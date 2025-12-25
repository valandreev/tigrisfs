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
)

type CacheKey struct {
	InodeID fuseops.InodeID
	Offset  uint64
}

type CacheEntry struct {
	Key        CacheKey
	Size       int64
	AccessTime time.Time
	OnDisk     bool
}

type DiskCache struct {
	mu       sync.Mutex
	basePath string
	MaxSize  int64
	CurSize  int64
	lru      *list.List
	items    map[CacheKey]*list.Element
}

func NewDiskCache(path string, maxSizeGB int) (*DiskCache, error) {
	if path == "" {
		return nil, nil
	}
	dataPath := filepath.Join(path, "data")
	if err := os.MkdirAll(dataPath, 0700); err != nil {
		return nil, err
	}

	// Create subdirectories for sharding (00-ff)
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
		items:    make(map[CacheKey]*list.Element),
	}

	return dc, nil
}

func (c *DiskCache) getPath(key CacheKey) string {
	shard := fmt.Sprintf("%02x", key.InodeID%256)
	fileName := fmt.Sprintf("%d_%d", key.InodeID, key.Offset)
	return filepath.Join(c.basePath, shard, fileName)
}

func (c *DiskCache) Put(inodeID fuseops.InodeID, offset uint64, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := CacheKey{InodeID: inodeID, Offset: offset}
	size := int64(len(data))
	path := c.getPath(key)

	// Write to temp file first
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

	// Update Metadata/LRU
	el, exists := c.items[key]
	if exists {
		entry := el.Value.(*CacheEntry)
		c.CurSize -= entry.Size
		entry.Size = size
		entry.AccessTime = time.Now()
		c.lru.MoveToFront(el)
	} else {
		entry := &CacheEntry{
			Key:        key,
			Size:       size,
			AccessTime: time.Now(),
			OnDisk:     true,
		}
		el = c.lru.PushFront(entry)
		c.items[key] = el
	}
	c.CurSize += size

	c.evictIfNeeded()
	return nil
}

func (c *DiskCache) Get(inodeID fuseops.InodeID, offset uint64, out []byte) (int, error) {
	c.mu.Lock()
	key := CacheKey{InodeID: inodeID, Offset: offset}
	el, exists := c.items[key]
	if exists {
		c.lru.MoveToFront(el)
		el.Value.(*CacheEntry).AccessTime = time.Now()
	}
	c.mu.Unlock()

	// If we don't track it in LRU (e.g. after restart before scan),
	// we still try to read it. If it exists, we add it to LRU.

	path := c.getPath(key)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) && exists {
			// Inconsistency: LRU says yes, Disk says no. Fix LRU.
			c.mu.Lock()
			if el, ok := c.items[key]; ok {
				c.removeEntry(el)
			}
			c.mu.Unlock()
		}
		return 0, err
	}
	defer f.Close()

	n, err := io.ReadFull(f, out)

	// If we successfully read, ensure it's in LRU
	if err == nil || err == io.ErrUnexpectedEOF {
		c.mu.Lock()
		if _, ok := c.items[key]; !ok {
			// Add to LRU
			entry := &CacheEntry{
				Key:        key,
				Size:       int64(n),
				AccessTime: time.Now(),
				OnDisk:     true,
			}
			c.items[key] = c.lru.PushFront(entry)
			c.items[key] = c.lru.PushFront(entry)
			c.CurSize += int64(n)
			// Potentially redundant size addition if we didn't track it initially?
			// On restart we should ideally scan or trust persistence.
		}
		c.mu.Unlock()
	}

	return n, err
}

func (c *DiskCache) evictIfNeeded() {
	// Accuracy +- 5%
	limit := c.MaxSize
	if c.CurSize <= limit {
		return
	}

	// Evict until we are below 95% of limit to avoid thrashing
	target := int64(float64(limit) * 0.95)

	for c.CurSize > target && c.lru.Len() > 0 {
		el := c.lru.Back()
		entry := el.Value.(*CacheEntry)

		// Remove file
		path := c.getPath(entry.Key)
		os.Remove(path) // Ignore error, maybe already gone

		c.removeEntry(el)
	}
}

func (c *DiskCache) removeEntry(el *list.Element) {
	entry := el.Value.(*CacheEntry)
	c.CurSize -= entry.Size
	delete(c.items, entry.Key)
	c.lru.Remove(el)
}

// RestoreState populates the LRU map from a list of known existing buffers.
// This should be called during LoadCache from persistence.
func (c *DiskCache) RestoreState(inodeID fuseops.InodeID, offset uint64, size int64, accessTime time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := CacheKey{InodeID: inodeID, Offset: offset}
	if _, exists := c.items[key]; exists {
		return
	}

	entry := &CacheEntry{
		Key:        key,
		Size:       size,
		AccessTime: accessTime,
		OnDisk:     true,
	}

	// We append to Back because RestoreState handles old items.
	// However, if we don't have stored access time, we assume they are 'old' relative to now?
	// If AccessTime is zero, use Now? No, that would make them 'new'.
	// Construct the list. Sorted by AccessTime outside?
	// Simple approach: PushBack.
	el := c.lru.PushBack(entry)
	c.items[key] = el
	c.CurSize += size
}

func (c *DiskCache) GetMeta(inodeID fuseops.InodeID, offset uint64) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := CacheKey{InodeID: inodeID, Offset: offset}
	if el, exists := c.items[key]; exists {
		return el.Value.(*CacheEntry).AccessTime, true
	}
	return time.Time{}, false
}
