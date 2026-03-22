//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tigrisdata/tigrisfs/core"
	"github.com/tigrisdata/tigrisfs/core/cfg"
)

func mountBucket(ctx context.Context, bucket string, flags *cfg.FlagStorage) (*core.Goofys, core.MountedFS, error) {
	return core.MountFuse(ctx, bucket, flags)
}

func checkFUSE() error {
	if _, err := os.Stat("/Library/Filesystems/macfuse.fs"); os.IsNotExist(err) {
		return fmt.Errorf("macFUSE is not installed.\n\nPlease install it from https://osxfuse.github.io")
	}
	return nil
}

func defaultMountRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "TigrisFS")
}

func platformConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "TigrisFS")
}
