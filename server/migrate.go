package main

// herdsman was called herdr-remote before. An install made under that name
// keeps working: its config (the token, so paired phones stay paired) moves
// to the new folder, and `herdsman install` retires the old login agent and
// binary so the two don't fight over the port.

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	oldName       = "herdr-remote"
	oldAgentLabel = "dev.akbxr.herdr-remote"
)

// migrateConfig moves ~/.config/herdr-remote to ~/.config/herdsman when only
// the old one is there. Reports whether it moved it.
func migrateConfig(home string) bool {
	if home == "" {
		return false
	}
	old := filepath.Join(home, ".config", oldName)
	cur := filepath.Join(home, ".config", "herdsman")
	if _, err := os.Stat(cur); err == nil {
		return false
	}
	if fi, err := os.Stat(old); err != nil || !fi.IsDir() {
		return false
	}
	return os.Rename(old, cur) == nil
}

// retireOldAgent stops and removes herdr-remote's login agent and binary,
// when they are there. Reports what it removed.
func retireOldAgent(home string) []string {
	var gone []string
	plist := filepath.Join(home, "Library", "LaunchAgents", oldAgentLabel+".plist")
	if _, err := os.Stat(plist); err == nil {
		launchctl("bootout", launchdTarget()+"/"+oldAgentLabel)
		if os.Remove(plist) == nil {
			gone = append(gone, plist)
		}
	}
	bin := filepath.Join(home, ".local", "bin", oldName)
	if _, err := os.Stat(bin); err == nil && os.Remove(bin) == nil {
		gone = append(gone, bin)
	}
	return gone
}

func announceMigration(home string) {
	if migrateConfig(home) {
		fmt.Fprintf(os.Stderr, "herdsman: moved the config of herdr-remote (its old name) to %s\n", filepath.Join(home, ".config", "herdsman"))
	}
}
