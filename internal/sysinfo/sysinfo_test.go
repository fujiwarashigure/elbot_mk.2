package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectorySizeAndCollect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "b.bin"), []byte("world!"), 0o644); err != nil {
		t.Fatal(err)
	}
	size, err := DirectorySize(dir)
	if err != nil {
		t.Fatalf("DirectorySize: %v", err)
	}
	if size != 11 {
		t.Fatalf("size = %d, want 11", size)
	}
	snapshot := Collect(dir)
	if snapshot.DirBytes != 11 || snapshot.Goroutines < 1 || snapshot.HeapSysBytes == 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if FormatBytes(2048) != "2.0 KB" {
		t.Fatalf("FormatBytes = %q", FormatBytes(2048))
	}
}
