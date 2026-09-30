//go:build !linux

package sysinfo

import "fmt"

// DiskUsage is only implemented on Linux; other platforms report an error so
// the report falls back to directory size only.
func DiskUsage(string) (uint64, uint64, error) {
	return 0, 0, fmt.Errorf("disk usage is not supported on this platform")
}
