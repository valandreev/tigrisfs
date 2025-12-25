package core

import (
	"container/list"
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

	dc := &DiskCache{
		basePath: dataPath,
		MaxSize:  int64(maxSizeGB) * 1024 * 1024 * 1024,
		lru:      list.New(),
		inodes:   make(map[fuseops.InodeID]*btree.BTreeG[*CacheEntry]),
		items:    make(map[CacheKey]*list.Element),
	}

	return dc, nil
}

func (c *DiskCache) getPath(inodeID fuseops.InodeID, physicalOffset uint64) string {
	shard := fmt.Sprintf("%02x", inodeID%256)
	fileName := fmt.Sprintf("%d_%d", inodeID, physicalOffset)
	return filepath.Join(c.basePath, shard, fileName)
}

func (c *DiskCache) Put(inodeID fuseops.InodeID, offset uint64, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

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

	// For Put, logical == physical
	c.addEntryLocked(inodeID, offset, offset, size, time.Now())

	c.evictIfNeeded()
	return nil
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
		tr = btree.NewBTreeG[*CacheEntry](cacheEntryLess)
		c.inodes[inodeID] = tr
	}
	tr.Set(entry)

	// Update LRU (shared for all logical entries pointing to same physical file)
	if el, exists := c.items[pKey]; exists {
		oldEntry := el.Value.(*CacheEntry)
		if accessTime.After(oldEntry.AccessTime) {
			oldEntry.AccessTime = accessTime
			c.lru.MoveToFront(el)
		}
	} else {
		el := c.lru.PushFront(entry)
		c.items[pKey] = el
		c.CurSize += size // Initial size of the physical file
	}
}

func (c *DiskCache) Get(inodeID fuseops.InodeID, offset uint64, out []byte) (int, error) {
	c.mu.Lock()
	tr, ok := c.inodes[inodeID]
	if !ok {
		c.mu.Unlock()
		return 0, os.ErrNotExist
	}

	var bestEntry *CacheEntry
	tr.Descend(&CacheEntry{LogicalOffset: offset}, func(item *CacheEntry) bool {
		if item.LogicalOffset <= offset && item.LogicalOffset+uint64(item.Size) > offset {
			bestEntry = item
		}
		return false
	})

	if bestEntry == nil {
		c.mu.Unlock()
		return 0, os.ErrNotExist
	}

	pKey := CacheKey{InodeID: inodeID, Offset: bestEntry.PhysicalOffset}
	el := c.items[pKey]
	c.lru.MoveToFront(el)
	bestEntry.AccessTime = time.Now()

	path := c.getPath(inodeID, bestEntry.PhysicalOffset)
	c.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			c.mu.Lock()
			if el, ok := c.items[pKey]; ok {
				c.removeEntry(el)
			}
			c.mu.Unlock()
		}
		return 0, err
	}
	defer f.Close()

	fileOffset := int64(offset - bestEntry.PhysicalOffset)
	if _, err := f.Seek(fileOffset, io.SeekStart); err != nil {
		return 0, err
	}

	n, err := io.ReadFull(f, out)
	if err == io.ErrUnexpectedEOF {
		err = nil
	}
	return n, err
}

func (c *DiskCache) evictIfNeeded() {
	limit := c.MaxSize
	if c.CurSize <= limit {
		return
	}

	target := int64(float64(limit) * 0.95)

	for c.CurSize > target && c.lru.Len() > 0 {
		el := c.lru.Back()
		c.removeEntry(el)
	}
}

func (c *DiskCache) removeEntry(el *list.Element) {
	entry := el.Value.(*CacheEntry)

	// Remove physical file
	path := c.getPath(entry.InodeID, entry.PhysicalOffset)
	os.Remove(path)

	c.CurSize -= entry.Size
	pKey := CacheKey{InodeID: entry.InodeID, Offset: entry.PhysicalOffset}
	delete(c.items, pKey)

	if tr, ok := c.inodes[entry.InodeID]; ok {
		tr.Delete(entry)
		if tr.Len() == 0 {
			delete(c.inodes, entry.InodeID)
		}
	}

	c.lru.Remove(el)
}

func (c *DiskCache) RestoreState(inodeID fuseops.InodeID, logicalOffset, physicalOffset uint64, size int64, accessTime time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addEntryLocked(inodeID, logicalOffset, physicalOffset, size, accessTime)
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
