//go:build windows
// +build windows

package files

import (
	"os"

	"golang.org/x/sys/windows"
)

func enableSparse(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var bytesReturned uint32
	err := windows.DeviceIoControl(handle, windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &bytesReturned, nil)
	if err != nil {
		if err == windows.ERROR_INVALID_FUNCTION || err == windows.ERROR_NOT_SUPPORTED {
			return nil
		}
		return err
	}
	return nil
}
