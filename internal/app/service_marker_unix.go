//go:build !windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// serviceMarker uses an OS file lock rather than trusting a stale PID. The
// kernel releases the lock automatically when the process exits, so a hard
// kill followed by a new container/host boot cannot be blocked by an old
// same-namespace PID.
type serviceMarker struct {
	path string
	file *os.File
}

func serviceMarkerRunning() bool {
	path := serviceMarkerPath()
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

func claimServiceMarker() (*serviceMarker, error) {
	path := serviceMarkerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create service marker directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open service marker: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			pid, _ := readServiceMarker(path)
			return nil, fmt.Errorf("elbot service already appears to be running with pid %d", pid)
		}
		return nil, fmt.Errorf("lock service marker: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("truncate service marker: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("seek service marker: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("write service marker: %w", err)
	}
	return &serviceMarker{path: path, file: file}, nil
}

func (m *serviceMarker) Close() error {
	if m == nil || m.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(m.file.Fd()), syscall.LOCK_UN)
	closeErr := m.file.Close()
	m.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
