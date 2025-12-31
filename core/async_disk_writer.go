package core

type DiskWriteRequest struct {
	inode  *Inode
	offset uint64
	data   []byte
}

func (fs *Goofys) AsyncDiskWrite(inode *Inode, offset uint64, data []byte) {
	fs.diskWriteCh <- &DiskWriteRequest{
		inode:  inode,
		offset: offset,
		data:   data,
	}
}

func (fs *Goofys) DiskWriter() {
	for req := range fs.diskWriteCh {
		if fs.diskCache == nil {
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
