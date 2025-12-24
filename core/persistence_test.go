package core

import (
	"context"
	"os"
	"testing"

	"github.com/jacobsa/fuse/fuseops"
	"github.com/stretchr/testify/assert"
	"github.com/tigrisdata/tigrisfs/core/cfg"
)

func TestPersistence(t *testing.T) {
	cachePath, err := os.MkdirTemp("", "tigris-cache-*")
	assert.NoError(t, err)
	defer os.RemoveAll(cachePath)

	flags := &cfg.FlagStorage{
		CachePath: cachePath,
		Uid:       1000,
		Gid:       1000,
		FileMode:  0644,
		DirMode:   0755,
	}

	ctx := context.Background()
	fs, err := newGoofys(ctx, "test-bucket", flags, func(string, *cfg.FlagStorage) (StorageBackend, error) {
		return &NilBackend{}, nil
	})
	assert.NoError(t, err)

	// Create some inodes
	fs.mu.Lock()
	root := fs.inodes[fuseops.RootInodeID]

	f1 := NewInode(fs, root, "file1")
	f1.Id = fs.nextInodeID
	fs.nextInodeID++
	f1.Attributes.Size = 100
	f1.knownSize = 100
	fs.inodes[f1.Id] = f1
	root.dir.Children = append(root.dir.Children, f1)

	d1 := NewInode(fs, root, "dir1")
	d1.Id = fs.nextInodeID
	fs.nextInodeID++
	d1.ToDir()
	fs.inodes[d1.Id] = d1
	root.dir.Children = append(root.dir.Children, d1)

	f2 := NewInode(fs, d1, "file2")
	f2.Id = fs.nextInodeID
	fs.nextInodeID++
	f2.Attributes.Size = 200
	f2.knownSize = 200
	fs.inodes[f2.Id] = f2
	d1.dir.Children = append(d1.dir.Children, f2)

	// Add a buffer to f2
	fb := &FileBuffer{
		offset: 0,
		length: 100,
		state:  BUF_CLEAN,
		onDisk: true,
	}
	f2.buffers.at.Set(fb.offset+fb.length, fb)
	f2.buffers.queue(fb)

	nextID := fs.nextInodeID
	fs.mu.Unlock()

	// Save
	err = fs.SaveCache()
	assert.NoError(t, err)

	// Create new fs and load
	fs2, err := newGoofys(ctx, "test-bucket", flags, func(string, *cfg.FlagStorage) (StorageBackend, error) {
		return &NilBackend{}, nil
	})
	assert.NoError(t, err)

	err = fs2.LoadCache()
	assert.NoError(t, err)

	// Verify
	fs2.mu.RLock()
	defer fs2.mu.RUnlock()

	assert.Equal(t, nextID, fs2.nextInodeID)
	assert.Len(t, fs2.inodes, 4) // Root, f1, d1, f2

	root2 := fs2.inodes[fuseops.RootInodeID]
	assert.NotNil(t, root2)
	assert.Len(t, root2.dir.Children, 2)

	var rf1, rd1 *Inode
	for _, child := range root2.dir.Children {
		if child.Name == "file1" {
			rf1 = child
		} else if child.Name == "dir1" {
			rd1 = child
		}
	}
	assert.NotNil(t, rf1)
	assert.NotNil(t, rd1)
	assert.Equal(t, uint64(100), rf1.Attributes.Size)

	assert.Len(t, rd1.dir.Children, 1)
	rf2 := rd1.dir.Children[0]
	assert.Equal(t, "file2", rf2.Name)
	assert.Equal(t, uint64(200), rf2.Attributes.Size)

	// Verify buffers
	assert.Equal(t, 1, rf2.buffers.Count())
	buf := rf2.buffers.Get(100)
	assert.NotNil(t, buf)
	assert.True(t, buf.onDisk)
	assert.Equal(t, uint64(0), buf.offset)
	assert.Equal(t, uint64(100), buf.length)
}

type NilBackend struct {
	StorageBackend
}

func (b *NilBackend) Init(name string) error { return nil }
func (b *NilBackend) MultipartExpire(i *MultipartExpireInput) (*MultipartExpireOutput, error) {
	return &MultipartExpireOutput{}, nil
}
