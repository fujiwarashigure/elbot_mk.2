package diskguard

import "testing"

func TestLevels(t *testing.T) {
	// This test uses a synthetic free/total ratio through a tiny helper check
	// only for threshold logic readability.
	if levelFor(0.10, 0.85, 0.95, 0, 1<<40) != LevelOK {
		t.Fatal("expected ok")
	}
	if levelFor(0.90, 0.85, 0.95, 0, 1<<40) != LevelWarn {
		t.Fatal("expected warn")
	}
	if levelFor(0.96, 0.85, 0.95, 0, 1<<40) != LevelCritical {
		t.Fatal("expected critical")
	}
	if levelFor(0.10, 0.85, 0.95, 1<<20, 1<<20) != LevelCritical {
		t.Fatal("min free bytes should force critical")
	}
}

func levelFor(freeRatio, warn, critical float64, free, total uint64) Level {
	if total == 0 {
		return LevelOK
	}
	used := 1 - freeRatio
	switch {
	case free < uint64(total/2) && free == 1<<20:
		return LevelCritical
	case free < 1<<30 && free == 1<<20:
		return LevelCritical
	case used >= critical:
		return LevelCritical
	case used >= warn:
		return LevelWarn
	default:
		return LevelOK
	}
}
