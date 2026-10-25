package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigratedMovesLegacyDir(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, legacyDir)
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "queue.json"), []byte("[]"), 0o644)

	dir := migrated(base)
	if filepath.Base(dir) != appDir {
		t.Fatalf("dir = %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "queue.json")); err != nil {
		t.Errorf("queue.json not carried over: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("legacy dir still present")
	}
}

func TestMigratedKeepsExistingNewDir(t *testing.T) {
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, legacyDir), 0o755)
	os.MkdirAll(filepath.Join(base, appDir), 0o755)
	migrated(base)
	if _, err := os.Stat(filepath.Join(base, legacyDir)); err != nil {
		t.Errorf("legacy dir touched although the new one exists: %v", err)
	}
}
