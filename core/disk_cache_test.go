package core

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDiskCache_LRU(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, err := NewDiskCache(tmpDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	dc.MaxSize = 10 * 1024

	// Put some data
	data1 := make([]byte, 4096)
	for i := range data1 {
		data1[i] = '1'
	}
	err = dc.Put(1, 0, data1)
	assert.NoError(t, err)

	data2 := make([]byte, 4096)
	for i := range data2 {
		data2[i] = '2'
	}
	err = dc.Put(2, 0, data2)
	assert.NoError(t, err)

	assert.Equal(t, int64(8192), dc.CurSize)

	data3 := make([]byte, 4096)
	for i := range data3 {
		data3[i] = '3'
	}
	err = dc.Put(3, 0, data3)
	assert.NoError(t, err)

	// Inode 1 should be gone
	readBuf := make([]byte, 4096)
	n, err := dc.Get(1, 0, readBuf)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err))
	assert.Equal(t, 0, n)

	// Inodes 2 and 3 should be there
	n, err = dc.Get(2, 0, readBuf)
	assert.NoError(t, err)
	assert.Equal(t, 4096, n)
	assert.Equal(t, byte('2'), readBuf[0])

	n, err = dc.Get(3, 0, readBuf)
	assert.NoError(t, err)
	assert.Equal(t, 4096, n)
	assert.Equal(t, byte('3'), readBuf[0])

	assert.Equal(t, int64(8192), dc.CurSize)
}

func TestDiskCache_RestoreState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_test_restore")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, _ := NewDiskCache(tmpDir, 1)

	now := time.Now()
	dc.RestoreState(10, 0, 1024, now)
	dc.RestoreState(10, 1024, 2048, now.Add(time.Minute))

	assert.Equal(t, int64(1024+2048), dc.CurSize)
	assert.Equal(t, 2, dc.lru.Len())

	// Front is the one restored LAST in our current implementation (PushBack)
	// Wait, RestoreState uses PushBack. So the one added last is at the Back?
	// Let's check RestoreState code: el := c.lru.PushBack(entry)
	// So 1024 is at front, 2048 is at back.
	assert.Equal(t, int64(1024), dc.lru.Front().Value.(*CacheEntry).Size)
}
