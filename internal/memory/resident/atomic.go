package resident

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic durably replaces path with data without ever exposing a
// half-written file: it writes a sibling temp file, fsyncs it, renames it over
// the target, then best-effort fsyncs the parent directory. A crash or power
// loss either leaves the old complete file or the new complete file, never a
// truncated memories.toml.
//
// The temp file lives in the same directory as the target so the final rename
// stays on one filesystem.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create resident memory dir %q: %w", dir, err)
	}
	// Preserve an operator-set mode (for example 0600) instead of resetting it
	// to the default on every atomic replace.
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp resident memory: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp resident memory: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp resident memory: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("chmod temp resident memory: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp resident memory: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace resident memory: %w", err)
	}
	cleanup = false
	syncDirBestEffort(dir)
	return nil
}

// syncDirBestEffort persists the rename itself. Directory fsync is not
// supported on every platform (notably Windows), so failures are ignored.
func syncDirBestEffort(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}
