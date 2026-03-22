package core

import "sync/atomic"

type DiskWriteRequest struct {
	inode  *Inode
	offset uint64
	data   []byte
}

func (fs *Goofys) AsyncDiskWrite(inode *Inode, offset uint64, data []byte) {
	if atomic.LoadInt32(&fs.shutdown) != 0 {
		return
	}
	req := &DiskWriteRequest{
		inode:  inode,
		offset: offset,
		data:   data,
	}
	select {
	case fs.diskWriteCh <- req:
	case <-fs.shutdownCh:
	}
}

func (fs *Goofys) DiskWriter() {
	for {
		select {
		case <-fs.shutdownCh:
			return
		case req := <-fs.diskWriteCh:
			if req == nil || fs.diskCache == nil {
				continue
			}
			err := fs.diskCache.Put(req.inode.Id, req.offset, req.data, true)
			if err == nil {
				req.inode.MarkBufferOnDisk(req.offset, uint64(len(req.data)))
			} else {
				mainLog.Warnf("Async disk write failed for inode %v: %v", req.inode.Id, err)
			}
		}
	}
}
