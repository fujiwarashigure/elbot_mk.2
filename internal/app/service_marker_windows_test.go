//go:build windows

package app

import "testing"

func TestServiceMarkerNamedMutexConcurrentClaim(t *testing.T) {
	first, err := claimServiceMarker()
	if err != nil {
		t.Fatalf("first claimServiceMarker() error = %v", err)
	}
	if !serviceMarkerRunning() {
		t.Fatal("serviceMarkerRunning() = false while the mutex is held")
	}
	if second, err := claimServiceMarker(); err == nil {
		_ = second.Close()
		t.Fatal("second claimServiceMarker() unexpectedly succeeded")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if serviceMarkerRunning() {
		t.Fatal("serviceMarkerRunning() = true after the mutex was closed")
	}
}
