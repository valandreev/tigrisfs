package index_test

import (
	"testing"

	"github.com/valandreev/tigrisfs/pkg/cache/index/indextest"
)

func TestCacheIndexContractWithMemoryIndex(t *testing.T) {
	indextest.RunCacheIndexContract(t, indextest.MemoryIndexFactory())
}
