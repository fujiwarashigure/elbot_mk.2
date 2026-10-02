package app

import (
	"strings"
	"testing"

	"elbot/internal/health"
)

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
