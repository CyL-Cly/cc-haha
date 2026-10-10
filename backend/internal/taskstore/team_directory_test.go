package taskstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTeamDirectoryRemovalRequiresSameLiveTransaction(t *testing.T) {
	s, err := New(Options{ConfigDir: t.TempDir(), PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureDirectory("actual"); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(s.Directory("actual"), "proof")
	if err = os.WriteFile(marker, []byte("private physical state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveTeamDirectory("actual"); err == nil {
		t.Fatal("unheld transaction removed directory")
	}
	var retained *Service
	if err = s.WithTeamLifecycleTransaction("actual", func(scoped *Service) error {
		retained = scoped
		if err := scoped.RemoveTeamDirectory("foreign"); err == nil {
			t.Error("foreign directory admitted")
		}
		return scoped.RemoveTeamDirectory("actual")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("actual held removal did not delete physical state", err)
	}
	if err = s.EnsureDirectory("actual"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(marker, []byte("new incarnation"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = retained.RemoveTeamDirectory("actual"); err == nil {
		t.Fatal("retained transaction removed new state")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "new incarnation" {
		t.Fatal("new state changed", err)
	}
}
