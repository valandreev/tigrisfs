//go:build windows

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
	return core.MountWin(ctx, bucket, flags)
}

func checkFUSE() error {
	// WinFSP installs to Program Files by default
	winfsp := filepath.Join(os.Getenv("ProgramFiles"), "WinFsp", "bin", "winfsp-x64.dll")
	winfspAlt := filepath.Join(os.Getenv("ProgramFiles(x86)"), "WinFsp", "bin", "winfsp-x64.dll")
	if _, err := os.Stat(winfsp); os.IsNotExist(err) {
		if _, err := os.Stat(winfspAlt); os.IsNotExist(err) {
			return fmt.Errorf("WinFSP is not installed.\n\nPlease install it from https://winfsp.dev")
		}
	}
	return nil
}

func defaultMountRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "TigrisFS")
}

func platformConfigDir() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "TigrisFS")
}
