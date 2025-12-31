package core

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jacobsa/fuse/fuseops"
)

func TestDiskCache_ConcurrentPuts(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, err := NewDiskCache(tmpDir, 1) // 1GB
	if err != nil {
		t.Fatal(err)
	}

	inodeID := fuseops.InodeID(100)
	offset := uint64(0)
	concurrency := 20

	var wg sync.WaitGroup
	wg.Add(concurrency)

	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(id int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("data from writer %d", id))
			// Add some randomness to sleep to simulate real world racing
			time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
			err := dc.Put(inodeID, offset, data, false)
			if err != nil {
				errCh <- fmt.Errorf("writer %d failed: %v", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("Concurrent Put failed: %v", err)
	}

	// Verify we can read the file
	readData := make([]byte, 100)
	n, err := dc.Get(inodeID, offset, offset, readData)
	if err != nil {
		t.Fatalf("Failed to read back data: %v", err)
	}
	t.Logf("Read back %d bytes: %s", n, string(readData[:n]))
}

func TestDiskCache_ConcurrentPutAndGet(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "disk_cache_test_getput")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, err := NewDiskCache(tmpDir, 1)
	if err != nil {
		t.Fatal(err)
	}

	inodeID := fuseops.InodeID(200)
	offset := uint64(0)
	data := []byte("some data to write")

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			err := dc.Put(inodeID, offset, data, false)
			if err != nil {
				t.Errorf("Put failed: %v", err)
			}
			// Simulate eviction or overwrite to force re-write logic if needed
			// But Put handles updates too.
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Reader
	go func() {
		defer wg.Done()
		buf := make([]byte, len(data))
		for i := 0; i < 50; i++ {
			_, err := dc.Get(inodeID, offset, offset, buf)
			if err != nil && !os.IsNotExist(err) {
				// IsNotExist is fine if Put hasn't happened yet
				t.Errorf("Get failed: %v", err)
			}
			time.Sleep(1 * time.Millisecond)
		}
	}()

	wg.Wait()
}
