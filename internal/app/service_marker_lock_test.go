//go:build !windows

package app

import (
	"runtime"
	"testing"
)

func TestServiceMarkerLockDetectsConcurrentClaim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("service marker lock is disabled on windows")
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	first, err := claimServiceMarker()
	if err != nil {
		t.Fatalf("first claimServiceMarker() error = %v", err)
	}
	if !serviceMarkerRunning() {
		t.Fatal("serviceMarkerRunning() = false while first claim is held")
	}
	if second, err := claimServiceMarker(); err == nil {
		_ = second.Close()
		t.Fatal("second claimServiceMarker() unexpectedly succeeded")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if serviceMarkerRunning() {
		t.Fatal("serviceMarkerRunning() = true after first marker was closed")
	}
}
