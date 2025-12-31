package core

import (
	"container/list"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jacobsa/fuse/fuseops"
	"github.com/tidwall/btree"
)

type CacheKey struct {
	InodeID fuseops.InodeID
	Offset  uint64
}

type CacheEntry struct {
	InodeID        fuseops.InodeID
	LogicalOffset  uint64
	PhysicalOffset uint64
	Size           int64
	AccessTime     time.Time
}

func cacheEntryLess(a, b *CacheEntry) bool {
	return a.LogicalOffset < b.LogicalOffset
}

type DiskCache struct {
	mu       sync.Mutex
	basePath string
	MaxSize  int64
	CurSize  int64
	lru      *list.List
	// Per-inode btree of entries, sorted by logical offset
	inodes map[fuseops.InodeID]*btree.BTreeG[*CacheEntry]
	// Map to find LRU element for any physical cache file (InodeID, PhysicalOffset)
	items map[CacheKey]*list.Element
	// Back-index to find all logical entries for a physical file
	logicalOffsets map[CacheKey]map[uint64]bool
	// Files that cannot be evicted (e.g. dirty buffers)
	pinned map[CacheKey]int

	// In-flight writes to deduplicate concurrent Puts for same key
	inflightWrites map[CacheKey]chan struct{}

	DiskUsageChecker func(path string) (uint64, uint64, error)
	Disabled         bool
}

func NewDiskCache(path string, maxSizeGB int) (*DiskCache, error) {
	if path == "" {
		return nil, nil
	}
	dataPath := filepath.Join(path, "data")
	if err := os.MkdirAll(dataPath, 0700); err != nil {
		return nil, err
	}

	for i := 0; i < 256; i++ {
		shardPath := filepath.Join(dataPath, fmt.Sprintf("%02x", i))
		if err := os.MkdirAll(shardPath, 0700); err != nil {
			return nil, err
		}
	}

	if maxSizeGB <= 0 {
		maxSizeGB = 10
	}

	dc := &DiskCache{
		basePath:         dataPath,
		MaxSize:          int64(maxSizeGB) * 1024 * 1024 * 1024,
		lru:              list.New(),
		inodes:           make(map[fuseops.InodeID]*btree.BTreeG[*CacheEntry]),
		items:            make(map[CacheKey]*list.Element),
		logicalOffsets:   make(map[CacheKey]map[uint64]bool),
		pinned:           make(map[CacheKey]int),
		inflightWrites:   make(map[CacheKey]chan struct{}),
		DiskUsageChecker: GetDiskFreeSpace,
	}

	return dc, nil
}

func (c *DiskCache) getPath(inodeID fuseops.InodeID, physicalOffset uint64) string {
	shard := fmt.Sprintf("%02x", inodeID%256)
	fileName := fmt.Sprintf("%d_%d", inodeID, physicalOffset)
	return filepath.Join(c.basePath, shard, fileName)
}

func (c *DiskCache) Put(inodeID fuseops.InodeID, offset uint64, data []byte, pinned bool) error {
	pKey := CacheKey{InodeID: inodeID, Offset: offset}

	c.mu.Lock()
	for {
		if el, exists := c.items[pKey]; exists {
			entry := el.Value.(*CacheEntry)
			entry.AccessTime = time.Now()
			c.lru.MoveToFront(el)
			if pinned {
				c.pinned[pKey]++
			}
			c.mu.Unlock()
			return nil
		}

		if c.Disabled {
			c.mu.Unlock()
			return fmt.Errorf("disk cache is disabled due to low space")
		}

		if ch, ok := c.inflightWrites[pKey]; ok {
			c.mu.Unlock()
			<-ch
			c.mu.Lock()
			continue
		}
		break
	}

	doneCh := make(chan struct{})
	c.inflightWrites[pKey] = doneCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.inflightWrites, pKey)
		close(doneCh)
		c.mu.Unlock()
	}()

	size := int64(len(data))
	path := c.getPath(inodeID, offset)

	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	f.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check again if it was added while we were doing I/O
	if el, exists := c.items[pKey]; exists {
		entry := el.Value.(*CacheEntry)
		entry.AccessTime = time.Now()
		c.lru.MoveToFront(el)
		if pinned {
			c.pinned[pKey]++
		}
		return nil
	}

	// For Put, logical == physical
	c.addEntryLocked(inodeID, offset, offset, size, time.Now())
	if pinned {
		c.pinned[pKey]++
	}

	c.evictIfNeeded()
	return nil
}

func (c *DiskCache) CanWrite() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.Disabled
}

func (c *DiskCache) addEntryLocked(inodeID fuseops.InodeID, logicalOffset, physicalOffset uint64, size int64, accessTime time.Time) {
	// LRU is tracked per PHYSICAL file
	pKey := CacheKey{InodeID: inodeID, Offset: physicalOffset}

	entry := &CacheEntry{
		InodeID:        inodeID,
		LogicalOffset:  logicalOffset,
		PhysicalOffset: physicalOffset,
		Size:           size,
		AccessTime:     accessTime,
	}

	// Update Inode BTree (for finding logical ranges)
	tr, ok := c.inodes[inodeID]
	if !ok {
		tr = btree.NewBTreeG(cacheEntryLess)
		c.inodes[inodeID] = tr
	}
	old, replaced := tr.Set(entry)
	if replaced {
		oldPKey := CacheKey{InodeID: inodeID, Offset: old.PhysicalOffset}
		if oldOffsets, ok := c.logicalOffsets[oldPKey]; ok {
			delete(oldOffsets, old.LogicalOffset)
		}
		// If we're replacing an entry that might have a different size,
		// we should theoretically adjust CurSize if the physical file changed.
		// However, in our system, physical files are immutable once written.
		// If physical offset changed, we need to handle that.
		if old.PhysicalOffset != physicalOffset {
			// This case shouldn't really happen for the same logical offset in our current design
			// but for completeness:
			mainLog.Warnf("DiskCache: logical offset %v physical offset changed from %v to %v", logicalOffset, old.PhysicalOffset, physicalOffset)
		}
	}

	// Update LRU (shared for all logical entries pointing to same physical file)
	if el, exists := c.items[pKey]; exists {
		// Physical file already exists - just update access time
		// Don't update size as it would double-count when multiple logical
		// entries (e.g., after buffer splits) point to the same physical file
		oldEntry := el.Value.(*CacheEntry)
		if accessTime.After(oldEntry.AccessTime) {
			oldEntry.AccessTime = accessTime
			c.lru.MoveToFront(el)
		}
	} else {
		// New physical file - add to LRU
		el := c.lru.PushFront(entry)
		c.items[pKey] = el
		c.CurSize += size // Initial size of the physical file
	}

	// Update back-index of logical offsets for this physical file
	offsets, ok := c.logicalOffsets[pKey]
	if !ok {
		offsets = make(map[uint64]bool)
		c.logicalOffsets[pKey] = offsets
	}
	offsets[logicalOffset] = true
}

func (c *DiskCache) Get(inodeID fuseops.InodeID, logicalOffset, physicalOffset uint64, data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	pKey := CacheKey{InodeID: inodeID, Offset: physicalOffset}
	el, ok := c.items[pKey]
	if !ok {
		return 0, os.ErrNotExist
	}

	entry := el.Value.(*CacheEntry)
	c.lru.MoveToFront(el)
	entry.AccessTime = time.Now()

	path := c.getPath(inodeID, physicalOffset)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// If the file is not found, remove the entry from cache metadata
			// Note: This assumes removeEntry can handle being called with inodeID and physicalOffset
			// If removeEntry expects *list.Element, this will need adjustment.
			// For now, let's assume a helper or modified removeEntry exists.
			// A safer approach might be to find the element first:
			if el, ok := c.items[pKey]; ok {
				c.removeEntry(el)
			}
		}
		return 0, err
	}
	defer f.Close()

	fileOffset := int64(logicalOffset - physicalOffset)
	n, err := f.ReadAt(data, fileOffset)
	if n > 0 && (err == nil || err == io.EOF) {
		return n, nil
	}
	return n, err
}

func (c *DiskCache) Pin(inodeID fuseops.InodeID, physicalOffset uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pKey := CacheKey{InodeID: inodeID, Offset: physicalOffset}
	c.pinned[pKey]++
}

func (c *DiskCache) Unpin(inodeID fuseops.InodeID, physicalOffset uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pKey := CacheKey{InodeID: inodeID, Offset: physicalOffset}
	if v, ok := c.pinned[pKey]; ok {
		if v <= 1 {
			delete(c.pinned, pKey)
		} else {
			c.pinned[pKey] = v - 1
		}
	}
}

func (c *DiskCache) GetThrottleDelay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Disabled {
		return 0
	}

	// If cache is > 95% full, start throttling
	if c.CurSize > int64(float64(c.MaxSize)*0.95) {
		return 100 * time.Millisecond
	}

	// Disk space check is done periodically in startMonitor,
	// but we could also check a cached value here if we updated it more often.
	// For now, let's just use the current size as the main throttle.
	return 0
}

func (c *DiskCache) evictIfNeeded() {
	c.evictToSize(c.MaxSize)
}

func (c *DiskCache) evictToSize(targetSize int64) {
	if c.lru.Len() == 0 {
		return
	}

	// We might not be able to reach targetSize if many files are pinned
	maxAttempts := c.lru.Len()
	for c.CurSize > targetSize && maxAttempts > 0 {
		el := c.lru.Back()
		entry := el.Value.(*CacheEntry)
		pKey := CacheKey{InodeID: entry.InodeID, Offset: entry.PhysicalOffset}

		if c.pinned[pKey] > 0 {
			// Move to front so we don't keep trying the same pinned item
			c.lru.MoveToFront(el)
			maxAttempts--
			continue
		}

		c.removeEntry(el)
		maxAttempts--
	}
}

// StartMonitor periodically checks disk usage and evicts if free space is low.
func (c *DiskCache) StartMonitor(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.checkDiskSpace()
			}
		}
	}()
}

func (c *DiskCache) checkDiskSpace() {
	if c.DiskUsageChecker == nil {
		return
	}

	free, total, err := c.DiskUsageChecker(c.basePath)
	if err != nil {
		return // Ignore errors, retry next time
	}

	// User req: "always remain 5-10% free space"
	targetFree := uint64(float64(total) * 0.10)
	criticalFree := uint64(float64(total) * 0.05)

	c.mu.Lock()
	defer c.mu.Unlock()

	if free < targetFree {
		if free < criticalFree {
			if !c.Disabled {
				mainLog.Warnf("Disk cache disabled: free space (%.2f GB) is below 5%% of total (%.2f GB)", float64(free)/1e9, float64(total)/1e9)
				c.Disabled = true
			}
			// If free space is very low, evict everything
			c.evictToSize(0)
		} else {
			// We need to free up (targetFree - free) bytes
			needed := int64(targetFree - free)

			// Reduce cache size by 'needed' amount, but don't go below 0
			newTarget := c.CurSize - needed
			if newTarget < 0 {
				newTarget = 0
			}

			// Also enforce MaxSize just in case
			if newTarget > c.MaxSize {
				newTarget = c.MaxSize
			}

			c.evictToSize(newTarget)
		}
	} else if c.Disabled && free > targetFree {
		mainLog.Infof("Disk cache re-enabled: free space (%.2f GB) is above 10%%", float64(free)/1e9)
		c.Disabled = false
	}
}

// EnsureSizeLimit checks if current size > MaxSize and evicts if needed.
// This is useful at startup if the configured size has changed.
func (c *DiskCache) EnsureSizeLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictIfNeeded()
}

// CleanupOrphanedFiles deletes files from disk that are not in the metadata.
func (c *DiskCache) CleanupOrphanedFiles() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := 0; i < 256; i++ {
		shardPath := filepath.Join(c.basePath, fmt.Sprintf("%02x", i))
		files, err := os.ReadDir(shardPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			if filepath.Ext(name) == ".tmp" {
				os.Remove(filepath.Join(shardPath, name))
				continue
			}

			var inodeID uint64
			var physicalOffset uint64
			n, err := fmt.Sscanf(name, "%d_%d", &inodeID, &physicalOffset)
			if err != nil || n != 2 {
				// Not our file, delete it
				os.Remove(filepath.Join(shardPath, name))
				continue
			}

			pKey := CacheKey{InodeID: fuseops.InodeID(inodeID), Offset: physicalOffset}
			if _, exists := c.items[pKey]; !exists {
				// Orphaned file
				os.Remove(filepath.Join(shardPath, name))
			}
		}
	}
}

func (c *DiskCache) removeEntry(el *list.Element) {
	entry := el.Value.(*CacheEntry)
	pKey := CacheKey{InodeID: entry.InodeID, Offset: entry.PhysicalOffset}

	// Remove physical file
	path := c.getPath(entry.InodeID, entry.PhysicalOffset)
	os.Remove(path)
	c.CurSize -= entry.Size

	// Remove all logical entries from BTree that point to this physical file
	if tr, ok := c.inodes[entry.InodeID]; ok {
		if offsets, ok := c.logicalOffsets[pKey]; ok {
			for logicalOffset := range offsets {
				tr.Delete(&CacheEntry{LogicalOffset: logicalOffset})
			}
		}
		if tr.Len() == 0 {
			delete(c.inodes, entry.InodeID)
		}
	}

	delete(c.items, pKey)
	delete(c.logicalOffsets, pKey)
	c.lru.Remove(el)
}

func (c *DiskCache) Delete(inodeID fuseops.InodeID, logicalOffset, physicalOffset uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	pKey := CacheKey{InodeID: inodeID, Offset: physicalOffset}
	if offsets, ok := c.logicalOffsets[pKey]; ok {
		delete(offsets, logicalOffset)
		if tr, ok := c.inodes[inodeID]; ok {
			tr.Delete(&CacheEntry{LogicalOffset: logicalOffset})
		}
		if len(offsets) == 0 {
			if el, ok := c.items[pKey]; ok {
				c.removeEntry(el)
			}
		}
	}
}

func (c *DiskCache) RestoreState(inodeID fuseops.InodeID, logicalOffset, physicalOffset uint64, size int64, accessTime time.Time, pinned bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addEntryLocked(inodeID, logicalOffset, physicalOffset, size, accessTime)
	if pinned {
		pKey := CacheKey{InodeID: inodeID, Offset: physicalOffset}
		c.pinned[pKey]++
	}
}

func (c *DiskCache) GetMeta(inodeID fuseops.InodeID, logicalOffset uint64) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tr, ok := c.inodes[inodeID]
	if !ok {
		return time.Time{}, false
	}

	var found *CacheEntry
	tr.Descend(&CacheEntry{LogicalOffset: logicalOffset}, func(item *CacheEntry) bool {
		if item.LogicalOffset == logicalOffset {
			found = item
		}
		return false
	})

	if found != nil {
		return found.AccessTime, true
	}
	return time.Time{}, false
}
