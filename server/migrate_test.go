package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateConfig(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".config", "herdr-remote")
	os.MkdirAll(old, 0o700)
	os.WriteFile(filepath.Join(old, "token"), []byte("secret"), 0o600)

	if !migrateConfig(home) {
		t.Fatal("not moved")
	}
	if b, err := os.ReadFile(filepath.Join(home, ".config", "herdsman", "token")); err != nil || string(b) != "secret" {
		t.Errorf("token after the move: %q %v", b, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old folder still there: %v", err)
	}

	// The new one there already: the old one is left alone.
	os.MkdirAll(old, 0o700)
	if migrateConfig(home) {
		t.Error("moved over an existing config")
	}
	if migrateConfig(t.TempDir()) {
		t.Error("moved with nothing to move")
	}
}

func TestRetireOldAgentBinary(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin", "herdr-remote")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	if gone := retireOldAgent(home); len(gone) != 1 || gone[0] != bin {
		t.Errorf("gone: %v", gone)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Errorf("old binary still there")
	}
}
