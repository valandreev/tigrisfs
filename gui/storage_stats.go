package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tigrisdata/tigrisfs/core"
)

type fsUsage struct {
	Path  string
	Free  uint64
	Total uint64
	Err   error
}

func getFSUsage(path string) fsUsage {
	path = filepath.Clean(path)
	if path == "." || path == "" {
		path = string(os.PathSeparator)
	}
	free, total, err := core.GetDiskFreeSpace(path)
	if err != nil {
		parent := filepath.Dir(path)
		if parent != "" && parent != path {
			if pFree, pTotal, pErr := core.GetDiskFreeSpace(parent); pErr == nil {
				free = pFree
				total = pTotal
				err = nil
			}
		}
	}
	return fsUsage{
		Path:  path,
		Free:  free,
		Total: total,
		Err:   err,
	}
}

func getDirSize(path string) (uint64, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var total uint64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() && info.Size() > 0 {
			total += uint64(info.Size())
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func formatBytes(v uint64) string {
	const unit = 1024.0
	if v < 1024 {
		return fmt.Sprintf("%d B", v)
	}
	value := float64(v)
	suffixes := []string{"KB", "MB", "GB", "TB", "PB"}
	for i, suffix := range suffixes {
		value /= unit
		if value < unit || i == len(suffixes)-1 {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%d B", v)
}

func formatBytesRate(v float64) string {
	if v <= 0 {
		return "0 B/s"
	}
	return formatBytes(uint64(v)) + "/s"
}

func formatFSUsage(u fsUsage) string {
	if u.Err != nil || u.Total == 0 {
		return "N/A"
	}
	used := u.Total - u.Free
	pct := (float64(used) / float64(u.Total)) * 100
	return fmt.Sprintf("%s used / %s total (%.1f%%)", formatBytes(used), formatBytes(u.Total), pct)
}

func fsUsageFraction(u fsUsage) float64 {
	if u.Err != nil || u.Total == 0 || u.Free > u.Total {
		return 0
	}
	return float64(u.Total-u.Free) / float64(u.Total)
}
