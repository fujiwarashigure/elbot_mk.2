package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elbot/internal/health"
)

func TestFetchHealthSnapshotReportsDisabledHealthz(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	if _, err := fetchHealthSnapshot(context.Background(), server.URL, ""); !errors.Is(err, errHealthzDisabled) {
		t.Fatalf("fetchHealthSnapshot error = %v, want errHealthzDisabled", err)
	}
}

func TestFetchHealthSnapshotSendsConfiguredToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ops-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	snapshot, err := fetchHealthSnapshot(context.Background(), server.URL, "ops-secret")
	if err != nil || snapshot.Status != "ok" {
		t.Fatalf("fetchHealthSnapshot = %#v, %v", snapshot, err)
	}
}

func TestDoctorPlatformConnectionFailsDisconnectedPlatform(t *testing.T) {
	snapshot := health.Snapshot{
		Status: "degraded",
		Platforms: []health.PlatformStatus{
			{Name: "telegram", Connected: false},
		},
	}
	status, _, err := doctorPlatformConnection(snapshot, []string{"telegram"}, false)
	if status != "failed" || err == nil || !strings.Contains(err.Error(), "telegram") {
		t.Fatalf("doctorPlatformConnection() = status %q, err %v; want failed disconnected telegram", status, err)
	}
}

func TestDoctorPlatformConnectionPassesConnectedPlatform(t *testing.T) {
	snapshot := health.Snapshot{
		Status: "ok",
		Platforms: []health.PlatformStatus{
			{Name: "telegram", Connected: true},
		},
	}
	status, detail, err := doctorPlatformConnection(snapshot, []string{"telegram"}, false)
	if status != "passed" || err != nil || !strings.Contains(detail, "telegram=connected") {
		t.Fatalf("doctorPlatformConnection() = detail %q, status %q, err %v", detail, status, err)
	}
}

func TestDoctorRequirePlatformFailsMissingStatus(t *testing.T) {
	snapshot := health.Snapshot{Status: "ok"}
	status, _, err := doctorPlatformConnection(snapshot, []string{"telegram"}, true)
	if status != "failed" || err == nil {
		t.Fatalf("doctorPlatformConnection() = status %q, err %v; want missing status failure", status, err)
	}
}
