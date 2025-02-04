// Just a check.v1 wrapper to allow running selected tests with:
// go test -v internal_test.go lfru_btree_test.go lfru_btree.go

package core

import (
	"os"
	"testing"

	"github.com/yandex-cloud/geesefs/core/cfg"
	"github.com/yandex-cloud/geesefs/log"
	. "gopkg.in/check.v1"
)

var testLog = log.GetLogger("test")

func TestCheckSuites(t *testing.T) {
	TestingT(t)
}

func TestMain(m *testing.M) {
	cfg.InitLoggers(&cfg.FlagStorage{LogLevel: "warn", Foreground: true, LogFormat: "console"})

	log.DumpLoggers("TestMain")

	os.Exit(m.Run())
}
