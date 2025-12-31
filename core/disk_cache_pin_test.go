package core

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDiskCache_Pinning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_pin_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, err := NewDiskCache(tmpDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	dc.MaxSize = 5000 // Small cache

	// 1. Put data 1 and pin it (natively)
	data1 := make([]byte, 4000)
	copy(data1, "data1")
	err = dc.Put(1, 0, data1, true)
	assert.NoError(t, err)

	// 2. Put data 2 (exceeds MaxSize if data 1 is evicted, but it shouldn't be)
	data2 := make([]byte, 4000)
	copy(data2, "data2")
	err = dc.Put(2, 0, data2, true)
	assert.NoError(t, err)

	// Verify data 1 is STILL THERE despite being older, because it is pinned
	readBuf := make([]byte, 4000)
	n, err := dc.Get(1, 0, 0, readBuf)
	assert.NoError(t, err)
	assert.Equal(t, 4000, n)
	assert.Equal(t, "data1", string(readBuf[:5]))

	// Verify data 2 might have evicted itself or just stayed if we allow small overflow during pinned state
	// In our implementation, evictToSize skips pinned items.
	// CurSize should be 8000 now.
	assert.Equal(t, int64(8000), dc.CurSize)

	// 3. Unpin data 1
	dc.Unpin(1, 0)

	// 4. Put data 3 - this should now trigger eviction of data 1 or 2
	data3 := make([]byte, 2000)
	copy(data3, "data3")
	err = dc.Put(3, 0, data3, false)
	assert.NoError(t, err)

	// Data 1 should be gone now as it was the oldest and is now unpinned
	n, err = dc.Get(1, 0, 0, readBuf)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err))

	assert.LessOrEqual(t, dc.CurSize, dc.MaxSize+2000) // 4000 (data2) + 2000 (data3) = 6000
}

func TestDiskCache_Throttling(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_throttle_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, err := NewDiskCache(tmpDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	dc.MaxSize = 1000

	// Cache is empty, no throttle
	assert.Equal(t, time.Duration(0), dc.GetThrottleDelay())

	// Fill cache to 96%
	data := make([]byte, 960)
	err = dc.Put(1, 0, data, false)
	assert.NoError(t, err)

	assert.Equal(t, 100*time.Millisecond, dc.GetThrottleDelay())
}
