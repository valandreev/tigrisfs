//go:build !windows
// +build !windows

package files

import "os"

func enableSparse(_ *os.File) error {
	return nil
}
