//go:build windows

package app

type serviceMarker struct{}

func serviceMarkerRunning() bool { return false }

func claimServiceMarker() (*serviceMarker, error) { return &serviceMarker{}, nil }

func (m *serviceMarker) Close() error { return nil }
