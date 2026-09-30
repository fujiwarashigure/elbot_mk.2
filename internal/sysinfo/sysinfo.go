// Package sysinfo collects portable runtime resource snapshots for reports.
package sysinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Snapshot is one point-in-time resource snapshot.
type Snapshot struct {
	DirBytes       int64
	DiskTotalBytes uint64
	DiskFreeBytes  uint64
	DiskErr        string
	HeapAllocBytes uint64
	HeapSysBytes   uint64
	RSSBytes       uint64
	Goroutines     int
}

// Collect gathers directory size, disk usage and process memory usage.
func Collect(root string) Snapshot {
	var snapshot Snapshot
	if size, err := DirectorySize(root); err == nil {
		snapshot.DirBytes = size
	}
	if total, free, err := DiskUsage(root); err == nil {
		snapshot.DiskTotalBytes = total
		snapshot.DiskFreeBytes = free
	} else {
		snapshot.DiskErr = err.Error()
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	snapshot.HeapAllocBytes = stats.Alloc
	snapshot.HeapSysBytes = stats.Sys
	snapshot.Goroutines = runtime.NumGoroutine()
	snapshot.RSSBytes = processRSS()
	return snapshot
}

// DirectorySize sums regular file sizes under root.
func DirectorySize(root string) (int64, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0, nil
	}
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return total, err
	}
	return total, nil
}

// FormatBytes renders a byte count for humans.
func FormatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	size := float64(value)
	index := -1
	for size >= unit && index < len(units)-1 {
		size /= unit
		index++
	}
	if size >= 100 {
		return fmt.Sprintf("%.0f %s", size, units[index])
	}
	return fmt.Sprintf("%.1f %s", size, units[index])
}

// FormatUintBytes renders an unsigned byte count for humans.
func FormatUintBytes(value uint64) string {
	return FormatBytes(int64(value))
}
