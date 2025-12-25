package core

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/jacobsa/fuse/fuseops"
)

// Keys
const (
	KeyGlobalState = "global:state"
)

func inodeKey(id fuseops.InodeID) []byte {
	return []byte(fmt.Sprintf("inode:%v", id))
}

func childrenKey(id fuseops.InodeID) []byte {
	return []byte(fmt.Sprintf("dir:%v:children", id))
}

func buffersKey(id fuseops.InodeID) []byte {
	return []byte(fmt.Sprintf("file:%v:buffers", id))
}

type GlobalState struct {
	Version      string
	NextInodeID  fuseops.InodeID
	NextHandleID fuseops.HandleID
}

func (fs *Goofys) SaveCache() error {
	if fs.flags.CachePath == "" {
		return nil
	}
	cacheDir := filepath.Join(fs.flags.CachePath, "metadata_db")

	// Ensure cache directory exists
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return err
	}

	db, err := pebble.Open(cacheDir, &pebble.Options{})
	if err != nil {
		return err
	}
	defer db.Close()

	batch := db.NewBatch()
	defer batch.Close()

	fs.mu.RLock()
	globalState := GlobalState{
		Version:      "1.0",
		NextInodeID:  fs.nextInodeID,
		NextHandleID: fs.nextHandleID,
	}
	// Copy inodes list to avoid holding lock for too long
	inodes := make([]*Inode, 0, len(fs.inodes))
	for _, inode := range fs.inodes {
		inodes = append(inodes, inode)
	}
	fs.mu.RUnlock()

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(globalState); err != nil {
		return err
	}
	if err := batch.Set([]byte(KeyGlobalState), buf.Bytes(), pebble.NoSync); err != nil {
		return err
	}

	for _, inode := range inodes {
		if err := fs.saveInode(batch, inode); err != nil {
			mainLog.Warnf("Failed to save inode %v: %v", inode.Id, err)
		}
	}

	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}

	return nil
}

func (fs *Goofys) saveInode(batch *pebble.Batch, inode *Inode) error {
	inode.mu.Lock()
	defer inode.mu.Unlock()

	cp := InodeCheckpoint{
		Id:         inode.Id,
		Name:       inode.Name,
		Attributes: inode.Attributes,
		AttrTime:   inode.AttrTime,
		ExpireTime: inode.ExpireTime,
		IsDir:      inode.isDir(),
		CacheState: inode.CacheState,
	}

	if inode.Parent != nil {
		cp.ParentId = inode.Parent.Id
	}

	if inode.isDir() {
		cp.ListDone = inode.dir.listDone
		cp.ListMarker = inode.dir.listMarker

		childrenIds := make([]fuseops.InodeID, len(inode.dir.Children))
		for i, child := range inode.dir.Children {
			childrenIds[i] = child.Id
		}

		var b bytes.Buffer
		if err := gob.NewEncoder(&b).Encode(childrenIds); err != nil {
			return err
		}
		if err := batch.Set(childrenKey(inode.Id), b.Bytes(), pebble.NoSync); err != nil {
			return err
		}
	} else {
		cp.KnownSize = inode.knownSize
		cp.KnownETag = inode.knownETag

		var bufferCPs []BufferStateCheckpoint
		inode.buffers.at.Scan(func(end uint64, b *FileBuffer) bool {
			if b.onDisk {
				atime := time.Time{}
				if fs.diskCache != nil {
					if t, ok := fs.diskCache.GetMeta(inode.Id, b.offset); ok {
						atime = t
					}
				}
				bufferCPs = append(bufferCPs, BufferStateCheckpoint{
					Offset:     b.offset,
					Length:     b.length,
					State:      b.state,
					DirtyID:    b.dirtyID,
					OnDisk:     true,
					DiskOffset: b.diskOffset,
					AccessTime: atime,
				})
			}
			return true
		})

		if len(bufferCPs) > 0 {
			var b bytes.Buffer
			if err := gob.NewEncoder(&b).Encode(bufferCPs); err != nil {
				return err
			}
			if err := batch.Set(buffersKey(inode.Id), b.Bytes(), pebble.NoSync); err != nil {
				return err
			}
		}
	}

	cp.UserMetadata = inode.userMetadata

	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(cp); err != nil {
		return err
	}
	return batch.Set(inodeKey(inode.Id), b.Bytes(), pebble.NoSync)
}

func (fs *Goofys) LoadCache() error {
	if fs.flags.CachePath == "" {
		return nil
	}
	cacheDir := filepath.Join(fs.flags.CachePath, "metadata_db")

	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return nil
	}

	db, err := pebble.Open(cacheDir, &pebble.Options{})
	if err != nil {
		return err
	}
	defer db.Close()

	val, closer, err := db.Get([]byte(KeyGlobalState))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}

	var globalState GlobalState
	if err := gob.NewDecoder(bytes.NewReader(val)).Decode(&globalState); err != nil {
		closer.Close()
		return err
	}
	closer.Close()

	fs.mu.Lock()
	fs.nextInodeID = globalState.NextInodeID
	fs.nextHandleID = globalState.NextHandleID
	fs.mu.Unlock()

	iter, _ := db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("inode:"),
		UpperBound: []byte("inode;"),
	})
	defer iter.Close()

	loadedInodes := make(map[fuseops.InodeID]*Inode)
	checkpoints := make(map[fuseops.InodeID]InodeCheckpoint)

	for iter.First(); iter.Valid(); iter.Next() {
		val := iter.Value()
		var cp InodeCheckpoint
		if err := gob.NewDecoder(bytes.NewReader(val)).Decode(&cp); err != nil {
			mainLog.Warnf("Failed to decode inode checkpoint: %v", err)
			continue
		}

		if cp.Id == fuseops.RootInodeID {
			fs.mu.RLock()
			root := fs.inodes[fuseops.RootInodeID]
			fs.mu.RUnlock()
			if root != nil {
				applyCheckpoint(root, cp)
				loadedInodes[fuseops.RootInodeID] = root
				checkpoints[fuseops.RootInodeID] = cp
			}
			continue
		}

		inode := NewInode(fs, nil, cp.Name)
		inode.Id = cp.Id
		if cp.IsDir {
			inode.ToDir()
		}
		applyCheckpoint(inode, cp)
		loadedInodes[cp.Id] = inode
		checkpoints[cp.Id] = cp
	}

	fs.mu.Lock()
	for id, inode := range loadedInodes {
		if id == fuseops.RootInodeID {
			continue
		}
		cp := checkpoints[id]
		if parent, ok := loadedInodes[cp.ParentId]; ok {
			inode.Parent = parent
			fs.inodes[id] = inode
		}
	}
	fs.mu.Unlock()

	for id, inode := range loadedInodes {
		if inode.isDir() {
			val, closer, err := db.Get(childrenKey(id))
			if err == nil {
				var childrenIds []fuseops.InodeID
				if err := gob.NewDecoder(bytes.NewReader(val)).Decode(&childrenIds); err == nil {
					inode.mu.Lock()
					inode.dir.Children = make([]*Inode, 0, len(childrenIds))
					for _, childId := range childrenIds {
						if child, ok := loadedInodes[childId]; ok {
							inode.dir.Children = append(inode.dir.Children, child)
						}
					}
					inode.mu.Unlock()
				}
				closer.Close()
			}
		} else {
			val, closer, err := db.Get(buffersKey(id))
			if err == nil {
				var bufferCPs []BufferStateCheckpoint
				if err := gob.NewDecoder(bytes.NewReader(val)).Decode(&bufferCPs); err == nil {
					inode.mu.Lock()
					for _, bcp := range bufferCPs {
						if bcp.OnDisk {
							fb := &FileBuffer{
								offset:     bcp.Offset,
								length:     bcp.Length,
								state:      bcp.State,
								dirtyID:    bcp.DirtyID,
								onDisk:     true,
								diskOffset: bcp.DiskOffset,
							}
							inode.buffers.at.Set(fb.offset+fb.length, fb)
							inode.buffers.queue(fb)

							if fs.diskCache != nil {
								fs.diskCache.RestoreState(inode.Id, fb.offset, fb.diskOffset, int64(fb.length), bcp.AccessTime)
							}
						}
					}
					inode.mu.Unlock()
				}
				closer.Close()
			}
		}

		// Re-queue modified inodes
		if inode.CacheState != ST_CACHED && inode.CacheState != ST_DEAD {
			inode.mu.Lock()
			inode.SetCacheState(inode.CacheState)
			inode.mu.Unlock()
		}
	}

	return nil
}

func applyCheckpoint(inode *Inode, cp InodeCheckpoint) {
	inode.Attributes = cp.Attributes
	inode.AttrTime = cp.AttrTime
	inode.ExpireTime = cp.ExpireTime
	inode.userMetadata = cp.UserMetadata
	inode.CacheState = cp.CacheState
	if inode.isDir() {
		inode.dir.listDone = cp.ListDone
		inode.dir.listMarker = cp.ListMarker
	} else {
		inode.knownSize = cp.KnownSize
		inode.knownETag = cp.KnownETag
		inode.OnDisk = true // If we have an entry in DB, we assume it might have data on disk or just metadata persistence
	}
}
