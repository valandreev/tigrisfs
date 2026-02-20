package core

import (
	"fmt"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/jacobsa/fuse/fuseops"
)

const pinReadChunkSize = 8 * 1024 * 1024

type PinResult struct {
	Files          int
	Dirs           int
	BytesRead      uint64
	PinnedBuffers  int
	UnpinnedBuffer int
}

type PathPinState struct {
	Exists      bool
	IsDir       bool
	Pinned      bool
	Size        uint64
	CachedBytes uint64
	FullyCached bool
	Loading     bool
	Dirty       bool
}

func (fs *Goofys) isManuallyPinnedInode(inodeID fuseops.InodeID) bool {
	fs.pinMu.Lock()
	defer fs.pinMu.Unlock()
	_, ok := fs.manualPinned[inodeID]
	return ok
}

func (fs *Goofys) ensurePinnedInode(inodeID fuseops.InodeID) {
	fs.pinMu.Lock()
	defer fs.pinMu.Unlock()
	if fs.manualPinned[inodeID] == nil {
		fs.manualPinned[inodeID] = make(map[uint64]struct{})
	}
}

func (fs *Goofys) rememberPinnedOffset(inodeID fuseops.InodeID, diskOffset uint64) bool {
	fs.pinMu.Lock()
	defer fs.pinMu.Unlock()
	offsets, ok := fs.manualPinned[inodeID]
	if !ok {
		offsets = make(map[uint64]struct{})
		fs.manualPinned[inodeID] = offsets
	}
	if _, exists := offsets[diskOffset]; exists {
		return false
	}
	offsets[diskOffset] = struct{}{}
	return true
}

func (fs *Goofys) popPinnedOffsets(inodeID fuseops.InodeID) []uint64 {
	fs.pinMu.Lock()
	defer fs.pinMu.Unlock()
	offsetsMap, ok := fs.manualPinned[inodeID]
	if !ok {
		return nil
	}
	delete(fs.manualPinned, inodeID)
	offsets := make([]uint64, 0, len(offsetsMap))
	for offset := range offsetsMap {
		offsets = append(offsets, offset)
	}
	return offsets
}

func (fs *Goofys) normalizedRelativePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "/")
	return path
}

func (fs *Goofys) PathPinState(path string) (PathPinState, error) {
	var state PathPinState
	if fs == nil {
		return state, syscall.EIO
	}

	inode, err := fs.LookupPath(fs.normalizedRelativePath(path))
	if err != nil {
		return state, err
	}

	state.Exists = true
	inodeID := inode.Id

	inode.mu.Lock()
	state.IsDir = inode.isDir()
	state.Size = inode.Attributes.Size
	cacheState := inode.CacheState
	flushing := inode.IsFlushing > 0
	if !state.IsDir {
		inode.buffers.Ascend(0, func(end uint64, b *FileBuffer) (cont bool, changed bool) {
			if b.onDisk || b.ptr != nil || b.zero {
				state.CachedBytes += b.length
			}
			if b.loading {
				state.Loading = true
			}
			return true, false
		})
	}
	inode.mu.Unlock()

	state.Dirty = cacheState == ST_CREATED || cacheState == ST_MODIFIED || flushing
	if !state.IsDir {
		state.Pinned = fs.isManuallyPinnedInode(inodeID)
		if state.Size == 0 {
			state.FullyCached = true
		} else {
			state.FullyCached = state.CachedBytes >= state.Size
		}
	}

	return state, nil
}

func (fs *Goofys) PinPath(path string, recursive bool) (PinResult, error) {
	var result PinResult
	if fs == nil {
		return result, syscall.EIO
	}
	if fs.diskCache == nil {
		return result, fmt.Errorf("disk cache is disabled")
	}
	inode, err := fs.LookupPath(fs.normalizedRelativePath(path))
	if err != nil {
		return result, err
	}
	visited := make(map[fuseops.InodeID]bool)
	err = fs.pinInodeRecursive(inode, recursive, visited, &result)
	return result, err
}

func (fs *Goofys) UnpinPath(path string, recursive bool) (PinResult, error) {
	var result PinResult
	if fs == nil {
		return result, syscall.EIO
	}
	if fs.diskCache == nil {
		return result, fmt.Errorf("disk cache is disabled")
	}
	inode, err := fs.LookupPath(fs.normalizedRelativePath(path))
	if err != nil {
		return result, err
	}
	visited := make(map[fuseops.InodeID]bool)
	err = fs.unpinInodeRecursive(inode, recursive, visited, &result)
	return result, err
}

func (fs *Goofys) pinInodeRecursive(inode *Inode, recursive bool, visited map[fuseops.InodeID]bool, result *PinResult) error {
	if inode == nil {
		return syscall.ESTALE
	}
	if visited[inode.Id] {
		return nil
	}
	visited[inode.Id] = true
	if atomic.LoadInt32(&inode.CacheState) == ST_DEAD {
		return syscall.ESTALE
	}

	if inode.isDir() {
		result.Dirs++
		if !recursive {
			return syscall.EISDIR
		}
		children, err := fs.readChildren(inode)
		if err != nil {
			return err
		}
		for _, child := range children {
			err = fs.pinInodeRecursive(child, true, visited, result)
			if err != nil && err != syscall.ENOENT && err != syscall.ESTALE {
				return err
			}
		}
		return nil
	}

	result.Files++
	bytesRead, err := fs.prefetchFile(inode)
	result.BytesRead += bytesRead
	if err != nil {
		return err
	}
	pinned, err := fs.pinLoadedBuffers(inode)
	result.PinnedBuffers += pinned
	return err
}

func (fs *Goofys) unpinInodeRecursive(inode *Inode, recursive bool, visited map[fuseops.InodeID]bool, result *PinResult) error {
	if inode == nil {
		return syscall.ESTALE
	}
	if visited[inode.Id] {
		return nil
	}
	visited[inode.Id] = true
	if atomic.LoadInt32(&inode.CacheState) == ST_DEAD {
		return syscall.ESTALE
	}

	if inode.isDir() {
		result.Dirs++
		if !recursive {
			return syscall.EISDIR
		}
		children, err := fs.readChildren(inode)
		if err != nil {
			return err
		}
		for _, child := range children {
			err = fs.unpinInodeRecursive(child, true, visited, result)
			if err != nil && err != syscall.ENOENT && err != syscall.ESTALE {
				return err
			}
		}
		return nil
	}

	result.Files++
	offsets := fs.popPinnedOffsets(inode.Id)
	for _, offset := range offsets {
		fs.diskCache.Unpin(inode.Id, offset)
		result.UnpinnedBuffer++
	}
	return nil
}

func (fs *Goofys) readChildren(inode *Inode) ([]*Inode, error) {
	dh := inode.OpenDir()
	if dh == nil {
		return nil, syscall.ENOTDIR
	}
	defer func() { _ = dh.CloseDir() }()

	children := make([]*Inode, 0, 32)
	dh.mu.Lock()
	defer dh.mu.Unlock()
	dh.Seek(2)
	for {
		child, err := dh.ReadDir()
		if err != nil {
			return nil, mapAwsError(err)
		}
		if child == nil {
			break
		}
		children = append(children, child)
	}
	return children, nil
}

func (fs *Goofys) prefetchFile(inode *Inode) (uint64, error) {
	inode.mu.Lock()
	size := inode.Attributes.Size
	inode.mu.Unlock()

	var total uint64
	fh := NewFileHandle(inode)
	for offset := int64(0); offset < int64(size); {
		chunk := int64(pinReadChunkSize)
		remaining := int64(size) - offset
		if chunk > remaining {
			chunk = remaining
		}
		_, n, err := fh.ReadFile(offset, chunk)
		if err != nil {
			return total, mapAwsError(err)
		}
		if n <= 0 {
			break
		}
		offset += int64(n)
		total += uint64(n)
	}
	return total, nil
}

func (fs *Goofys) pinLoadedBuffers(inode *Inode) (int, error) {
	if fs.diskCache == nil {
		return 0, nil
	}

	fs.ensurePinnedInode(inode.Id)
	toFs := -1
	pinned := 0

	inode.mu.Lock()
	defer inode.mu.Unlock()
	inode.buffers.Ascend(0, func(end uint64, b *FileBuffer) (cont bool, changed bool) {
		if !b.onDisk && b.ptr != nil {
			fs.tryEvictToDisk(inode, b, &toFs)
		}
		if b.onDisk && fs.rememberPinnedOffset(inode.Id, b.diskOffset) {
			fs.diskCache.Pin(inode.Id, b.diskOffset)
			pinned++
		}
		return true, false
	})

	return pinned, nil
}
