//go:build windows

package app

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

const serviceMarkerMutexName = `Local\elbot_mk2_service_marker`

// serviceMarker uses a Windows named mutex, which the kernel releases
// automatically when the owning process exits. This gives native Windows
// builds the same stale-marker resistance as the POSIX flock implementation.
type serviceMarker struct {
	handle windows.Handle
}

func serviceMarkerRunning() bool {
	name, err := windows.UTF16PtrFromString(serviceMarkerMutexName)
	if err != nil {
		return false
	}
	handle, err := windows.OpenMutex(windows.MUTEX_MODIFY_STATE|windows.SYNCHRONIZE, false, name)
	if err != nil || handle == 0 {
		return false
	}
	_ = windows.CloseHandle(handle)
	return true
}

func claimServiceMarker() (*serviceMarker, error) {
	name, err := windows.UTF16PtrFromString(serviceMarkerMutexName)
	if err != nil {
		return nil, fmt.Errorf("encode service marker name: %w", err)
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("elbot service already appears to be running")
		}
		return nil, fmt.Errorf("create service marker: %w", err)
	}
	return &serviceMarker{handle: handle}, nil
}

func (m *serviceMarker) Close() error {
	if m == nil || m.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(m.handle)
	m.handle = 0
	return err
}
