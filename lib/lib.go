package lib

import "os"

func IsTTY(f *os.File) bool {
	fileInfo, _ := f.Stat()

	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}
