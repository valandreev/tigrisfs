package core

import (
	"time"

	"github.com/jacobsa/fuse/fuseops"
)

type BufferStateCheckpoint struct {
	Offset     uint64
	Length     uint64
	State      BufferState
	DirtyID    uint64
	OnDisk     bool
	DiskOffset uint64
	AccessTime time.Time
}

type InodeCheckpoint struct {
	Id         fuseops.InodeID
	Name       string
	ParentId   fuseops.InodeID
	Attributes InodeAttributes
	AttrTime   time.Time
	ExpireTime time.Time
	IsDir      bool
	CacheState int32

	// Directory specific
	ListDone   bool
	ListMarker string

	// File specific
	KnownSize    uint64
	KnownETag    string
	UserMetadata map[string][]byte
}
