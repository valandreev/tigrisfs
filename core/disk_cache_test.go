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
	err = os.WriteFile(dc.getPath(10, 0), make([]byte, 2048), 0600)
	assert.NoError(t, err)

	// logical 0, physical 0
	dc.RestoreState(10, 0, 0, 1024, now, false)
	// logical 1024, physical 0 (split)
	dc.RestoreState(10, 1024, 0, 2048, now.Add(time.Minute), false)

	assert.Equal(t, int64(2048), dc.CurSize) // Uses physical file size from disk
	assert.Equal(t, 1, dc.lru.Len())         // Same physical file = 1 LRU entry
}

func TestDiskCache_RestoreState_SkipsMissingFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_test_restore_missing")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, _ := NewDiskCache(tmpDir, 1)
	dc.RestoreState(10, 0, 0, 1024, time.Now(), false)

	assert.Equal(t, int64(0), dc.CurSize)
	assert.Equal(t, 0, dc.lru.Len())
}

func TestDiskCache_CheckDiskSpace_CriticalDoesNotEvictAll(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_test_critical")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, err := NewDiskCache(tmpDir, 1)
	assert.NoError(t, err)

	block := make([]byte, 4096)
	assert.NoError(t, dc.Put(1, 0, block, false))
	assert.NoError(t, dc.Put(2, 0, block, false))
	assert.NoError(t, dc.Put(3, 0, block, false))
	assert.Equal(t, int64(3*4096), dc.CurSize)

	// 4% free on total=100 => below 5% critical.
	// Target free is 10%, so only 6 bytes are needed.
	// We should evict oldest entries incrementally, not wipe the whole cache.
	dc.DiskUsageChecker = func(path string) (uint64, uint64, error) {
		return 4, 100, nil
	}
	dc.checkDiskSpace()

	assert.True(t, dc.Disabled)
	assert.Equal(t, int64(2*4096), dc.CurSize)

	readBuf := make([]byte, 1)
	_, err = dc.Get(1, 0, 0, readBuf)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err))

	_, err = dc.Get(2, 0, 0, readBuf)
	assert.NoError(t, err)
	_, err = dc.Get(3, 0, 0, readBuf)
	assert.NoError(t, err)
}

func TestDiskCache_CheckDiskSpace_ReenableHysteresis(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_test_reenable")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, err := NewDiskCache(tmpDir, 1)
	assert.NoError(t, err)

	dc.Disabled = true
	dc.DiskUsageChecker = func(path string) (uint64, uint64, error) {
		return 11, 100, nil
	}
	dc.checkDiskSpace()
	assert.True(t, dc.Disabled)

	dc.DiskUsageChecker = func(path string) (uint64, uint64, error) {
		return 13, 100, nil
	}
	dc.checkDiskSpace()
	assert.False(t, dc.Disabled)
}
