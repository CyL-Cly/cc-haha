package gitbranch

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func gitContextRun(t *testing.T, owner *cooperative.Owner, run func(*cooperative.Scope) error) {
	t.Helper()
	if e := owner.Run(context.Background(), run); e != nil {
		t.Fatal(e)
	}
}

func TestGitCWDContextActualWatcherInitThenComputeRereadsLiveContext(t *testing.T) {
	home := t.TempDir()
	gitDir := filepath.Join(home, ".git")
	if e := os.MkdirAll(gitDir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(gitDir, "HEAD")
	if e := syscall.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0600); e != nil {
		t.Fatal(e)
	}
	owner := cooperative.New()
	state, _ := bootstrapstate.New(owner, "A", home, func() (string, error) { return "B", nil })
	watch := &NativePollWatch{Stat: os.Stat}
	var mu sync.Mutex
	headReads := 0
	var contexts []string
	var service *Service
	var cleanups []func(*cooperative.Scope) error
	var e error
	service, e = New(Options{Owner: owner, CWD: func() string { return state.Snapshot().CWD }, Now: func() int64 { return 1700000000000 }, StatSync: os.Stat, Stat: os.Stat, ReadFile: func(path string) ([]byte, error) {
		if filepath.Base(path) == "HEAD" {
			mu.Lock()
			headReads++
			mu.Unlock()
		}
		return os.ReadFile(path)
	}, WatchFile: watch.Watch, ScrollDraining: func() bool { return false }, RegisterCleanup: func(cb func(*cooperative.Scope) error) { cleanups = append(cleanups, cb) }, NodeEnv: "production"})
	if e != nil {
		owner.Close()
		t.Fatal(e)
	}
	defer func() { _ = service.Close(context.Background()); watch.Join(); owner.Close() }()
	var future *BranchFuture
	gitContextRun(t, owner, func(scope *cooperative.Scope) error {
		token, e := state.Context(scope)
		if e != nil {
			return e
		}
		future, e = service.BranchWithCWD(scope, func(next *cooperative.Scope) (string, error) {
			value, e := token.GetCWD(next)
			contexts = append(contexts, value)
			return value, e
		})
		return e
	})
	// Native FIFO writer opening is the real first HEAD read barrier. It is not
	// an expected branch/Source phase inserted into the Go service.
	fd, e := os.OpenFile(path, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer fd.Close()
	if e = os.Rename(path, path+".first-fifo"); e != nil {
		t.Fatal(e)
	}
	originalHead := []byte("ref: refs/heads/original-private-branch\n")
	if e = os.WriteFile(path, originalHead, 0600); e != nil {
		t.Fatal(e)
	}
	gitContextRun(t, owner, func(scope *cooperative.Scope) error {
		return state.SetCWD(scope, filepath.Join(home, "late-shell-e\u0301"))
	})
	if _, e = fd.Write(originalHead); e != nil {
		t.Fatal(e)
	}
	if e = fd.Close(); e != nil {
		t.Fatal(e)
	}
	gitContextRun(t, owner, func(scope *cooperative.Scope) error {
		value, e := future.Await(scope)
		if e != nil {
			return e
		}
		if value != "original-private-branch" {
			t.Error(value)
		}
		if len(contexts) != 2 || contexts[0] != home || contexts[1] != filepath.Join(home, "late-shell-é") {
			t.Error("compute captured ensure cwd", contexts)
		}
		if len(cleanups) != 1 {
			t.Error("watch cleanup registration missing", len(cleanups))
		}
		return service.Reset(scope)
	})
	mu.Lock()
	reads := headReads
	mu.Unlock()
	if reads != 2 {
		t.Errorf("actual ensure/compute HEAD reads = %d", reads)
	}
}

func TestGitCWDContextAdmissionErrorsAndSharedOverrideToken(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	home := t.TempDir()
	state, _ := bootstrapstate.New(owner, "A", home, func() (string, error) { return "B", nil })
	nativeReads := 0
	service, e := New(Options{Owner: owner, CWD: func() string { t.Error("default getter substituted for explicit context"); return home }, Now: func() int64 { return 1 }, StatSync: func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }, Stat: os.Stat, ReadFile: func(string) ([]byte, error) { nativeReads++; return nil, fs.ErrNotExist }, WatchFile: func(string, time.Duration, func()) (func(), error) {
		t.Error("no Git root must not watch")
		return func() {}, nil
	}, ScrollDraining: func() bool { return false }, RegisterCleanup: func(func(*cooperative.Scope) error) { t.Error("no Git root must not register") }})
	if e != nil {
		t.Fatal(e)
	}
	defer service.Join()
	var token *bootstrapstate.CWDContext
	gitContextRun(t, owner, func(scope *cooperative.Scope) error {
		base, e := state.Context(scope)
		if e != nil {
			return e
		}
		return base.RunWithOverride(scope, "/override-e\u0301", func(next *bootstrapstate.CWDContext) error { token = next; return nil })
	})
	gitContextRun(t, foreign, func(scope *cooperative.Scope) error {
		if _, e := service.BranchWithCWD(scope, token.GetCWD); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		return nil
	})
	sentinel := errors.New("original getter error")
	gitContextRun(t, owner, func(scope *cooperative.Scope) error {
		if _, e := service.BranchWithCWD(scope, nil); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		fail, e := service.BranchWithCWD(scope, func(*cooperative.Scope) (string, error) { return "", sentinel })
		if e != nil {
			return e
		}
		if _, e = fail.Await(scope); !errors.Is(e, sentinel) {
			t.Error(e)
		}
		if e = service.Reset(scope); e != nil {
			return e
		}
		reads := []string{}
		future, e := service.BranchWithCWD(scope, func(next *cooperative.Scope) (string, error) {
			value, e := token.GetCWD(next)
			reads = append(reads, value)
			return value, e
		})
		if e != nil {
			return e
		}
		if e = state.SetCWD(scope, "/global-later"); e != nil {
			return e
		}
		value, e := future.Await(scope)
		if e != nil {
			return e
		}
		if value != "HEAD" || len(reads) != 2 || reads[0] != "/override-e\u0301" || reads[1] != "/override-e\u0301" {
			t.Error(value, reads)
		}
		if e = service.Reset(scope); e != nil {
			return e
		}
		calls := 0
		future, e = service.BranchWithCWD(scope, func(*cooperative.Scope) (string, error) {
			calls++
			if calls == 2 {
				return "", sentinel
			}
			return home, nil
		})
		if e != nil {
			return e
		}
		if _, e = future.Await(scope); !errors.Is(e, sentinel) {
			t.Error(e)
		}
		return nil
	})
	if nativeReads != 0 {
		t.Error("no-repo context used native branch FS")
	}
}
