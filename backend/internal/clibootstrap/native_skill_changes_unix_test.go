//go:build !windows

package clibootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedSkillSignalMissingConfigCanonicalIdentitySurvivesPhysicalFirstInstall(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual-home")
	alias := filepath.Join(root, "home-alias")
	if err := os.MkdirAll(actual, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(alias, "not-created-yet", ".claude")
	physical := filepath.Join(actual, "not-created-yet", ".claude")
	notified := 0
	unsubscribe := subscribeNativeSkillChanges(config, func() error { notified++; return nil })
	defer unsubscribe()
	if err := os.MkdirAll(physical, 0700); err != nil {
		t.Fatal(err)
	}
	if err := NotifyNativeSkillCommandsChanged(physical); err != nil || notified != 1 {
		t.Fatal("first directory creation changed managed config identity", err, notified)
	}
}
