package filehistorycopy

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRefAdmissionDefenses(t *testing.T) {
	owner, service, _, refs, _, _, _ := setup(t, OSFiles{})
	called := false
	work := func() error { called = true; return nil }
	if _, e := service.dispatch(nil, work); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if n, _ := refs.Snapshot(); n != 0 || called {
		t.Fatal("nil admission crossed physical boundary", n, called)
	}
	// Deliberate private corruption exercises helper defense only, not an
	// admitted public Service profile or Source business configuration.
	service.options.Owner = nil
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		future, e := service.dispatch(scope, work)
		if future != nil {
			return errors.New("partial binding created a producer")
		}
		return e
	}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if n, _ := refs.Snapshot(); n != 0 || called {
		t.Fatal("future-construction failure retained physical work", n, called)
	}
}

func TestNativeCopyRealOutputDirectoryRefusal(t *testing.T) {
	home := t.TempDir()
	source, target := filepath.Join(home, "file"), filepath.Join(home, "target-dir")
	if e := os.WriteFile(source, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(target, 0700); e != nil {
		t.Fatal(e)
	}
	if e := (OSFiles{}).CopyFile(source, target); e == nil {
		t.Fatal("actual directory target silently accepted")
	}
	info, e := os.Stat(target)
	if e != nil || !info.IsDir() {
		t.Fatal("failed physical copy changed target type", e)
	}
	bytes, e := os.ReadFile(source)
	if e != nil || string(bytes) != "private" {
		t.Fatal("failed copy changed original", e)
	}
}
