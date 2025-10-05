package index_test

import (
	"testing"

	"github.com/tigrisdata/tigrisfs/pkg/cache/index/indextest"
)

func TestCacheIndexContractWithMemoryIndex(t *testing.T) {
	indextest.RunCacheIndexContract(t, indextest.MemoryIndexFactory())
}
