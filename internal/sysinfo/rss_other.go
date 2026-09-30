//go:build !linux

package sysinfo

func processRSS() uint64 { return 0 }
