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
	err = dc.Put(1, 0, data1, false)
	assert.NoError(t, err)

	data2 := make([]byte, 4096)
	for i := range data2 {
		data2[i] = '2'
	}
	err = dc.Put(2, 0, data2, false)
	assert.NoError(t, err)

	assert.Equal(t, int64(8192), dc.CurSize)

	data3 := make([]byte, 4096)
	for i := range data3 {
		data3[i] = '3'
	}
	err = dc.Put(3, 0, data3, false)
	assert.NoError(t, err)

	// Inode 1 should be gone (First In, First Out in LRU)
	readBuf := make([]byte, 4096)
	n, err := dc.Get(1, 0, 0, readBuf)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err))
	assert.Equal(t, 0, n)

	// Inodes 2 and 3 should be there
	n, err = dc.Get(2, 0, 0, readBuf)
	assert.NoError(t, err)
	assert.Equal(t, 4096, n)
	assert.Equal(t, byte('2'), readBuf[0])

	n, err = dc.Get(3, 0, 0, readBuf)
	assert.NoError(t, err)
	assert.Equal(t, 4096, n)
	assert.Equal(t, byte('3'), readBuf[0])

	assert.Equal(t, int64(8192), dc.CurSize)
}

func TestDiskCache_SubRange(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_test_subrange")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, _ := NewDiskCache(tmpDir, 1)

	// Put 128KB block at offset 0
	data := make([]byte, 128*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	err = dc.Put(10, 0, data, false)
	assert.NoError(t, err)

	// Now mimic a split: add logical entry for [64K, 128K) pointing to physical file 0
	dc.RestoreState(10, 64*1024, 0, 64*1024, time.Now(), false)

	// Read 32KB from offset 64KB (logical)
	// It should find the entry starting at 64KB, which points to physical file 0.
	// The read from physical file 0 should be at offset 64KB.
	readBuf := make([]byte, 32*1024)
	n, err := dc.Get(10, 64*1024, 0, readBuf)
	assert.NoError(t, err)
	assert.Equal(t, 32*1024, n)
	assert.Equal(t, data[64*1024], readBuf[0])
	assert.Equal(t, data[64*1024+32*1023], readBuf[32*1023])
}

func TestDiskCache_RestoreState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_test_restore")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, _ := NewDiskCache(tmpDir, 1)

	now := time.Now()
	// logical 0, physical 0
	dc.RestoreState(10, 0, 0, 1024, now, false)
	// logical 1024, physical 0 (split)
	dc.RestoreState(10, 1024, 0, 2048, now.Add(time.Minute), false)

	assert.Equal(t, int64(1024), dc.CurSize) // Second RestoreState for same physical file doesn't increase size
	assert.Equal(t, 1, dc.lru.Len())         // Same physical file = 1 LRU entry
}
