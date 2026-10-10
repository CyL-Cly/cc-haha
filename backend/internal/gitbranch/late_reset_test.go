package gitbranch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// A private native HEAD read actually finishes, then its completion remains
// held. Reset runs on the same Owner before the real continuation resumes.
// This creates the Source await gap causally without sleep or a phase ordinal.
func TestLateResetRejectsNullRefDirectoryBeforeNativeWatch(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once, released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	h := newHarness(t, func(o *Options) {
		o.ReadFile = func(path string) ([]byte, error) {
			b, err := os.ReadFile(path)
			if filepath.Base(path) == "HEAD" {
				once.Do(func() { close(entered); <-release })
			}
			return b, err
		}
	})
	t.Cleanup(unblock)
	h.write(t, ".git/HEAD", "ref: refs/heads/second\n")
	var result *Future
	h.call(t, func(scope *cooperative.Scope) error {
		dir := filepath.Join(h.home, ".git")
		h.s.gitDir = &dir
		result = h.s.future()
		h.s.updateRef(scope, result, func(scope *cooperative.Scope, err error) { h.s.settle(scope, result, resultValue(err)) })
		return nil
	})
	<-entered
	err := h.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := h.s.Reset(scope); err != nil {
			return err
		}
		unblock()
		_, err := result.Await(scope)
		var named interface{ ErrorName() string }
		var coded interface{ ErrorCode() string }
		if !errors.As(err, &named) || !errors.As(err, &coded) || named.ErrorName() != "TypeError" || coded.ErrorCode() != "ERR_INVALID_ARG_TYPE" {
			return errors.New("null receiver became a relative native path instead of rejecting path.join")
		}
		if h.s.refPath != "" || h.s.branch != nil || len(h.s.watched) != 0 {
			return errors.New("null-path rejection continued mutating post-reset watcher/cache")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.notify) != 0 {
		t.Fatalf("late reset made native registrations: %v", h.notify)
	}
}

func resultValue(err error) result { return result{raw: raw(nil), err: err} }

func TestLateRefContinuationReadsCurrentNullishDirectoryAfterAwait(t *testing.T) {
	for _, mode := range []string{"git-fallback", "common-override", "detached-after-reset"} {
		t.Run(mode, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var held, released sync.Once
			unblock := func() { released.Do(func() { close(release) }) }
			h := newHarness(t, func(o *Options) {
				o.ReadFile = func(path string) ([]byte, error) {
					b, err := os.ReadFile(path)
					if filepath.Base(path) == "HEAD" {
						held.Do(func() { close(entered); <-release })
					}
					return b, err
				}
			})
			t.Cleanup(unblock)
			text := "ref: refs/heads/second\n"
			if mode == "detached-after-reset" {
				text = "0123456789012345678901234567890123456789\n"
			}
			h.write(t, ".git/HEAD", text)
			var future *Future
			h.call(t, func(scope *cooperative.Scope) error {
				original := filepath.Join(h.home, ".git")
				h.s.gitDir = &original
				future = h.s.future()
				h.s.updateRef(scope, future, func(scope *cooperative.Scope, err error) { h.s.settle(scope, future, resultValue(err)) })
				return nil
			})
			<-entered
			if err := h.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if err := h.s.Reset(scope); err != nil {
					return err
				}
				newGit := filepath.Join(h.home, "new-git")
				newCommon := filepath.Join(h.home, "new-common")
				expected := ""
				if mode != "detached-after-reset" {
					h.s.gitDir = &newGit
					if mode == "common-override" {
						h.s.commonDir = &newCommon
						expected = filepath.Join(newCommon, "refs/heads/second")
					} else {
						expected = filepath.Join(newGit, "refs/heads/second")
					}
				}
				unblock()
				if _, err := future.Await(scope); err != nil {
					return err
				}
				if h.s.refPath != expected || h.s.branch != nil {
					return errors.New("ref continuation captured an obsolete directory or recreated cache")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStartResetAtCommonDirectoryAwaitRejectsBeforeHeadWatch(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var held, released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	h := newHarness(t, func(o *Options) {
		o.ReadFile = func(path string) ([]byte, error) {
			b, err := os.ReadFile(path)
			if filepath.Base(path) == "commondir" {
				held.Do(func() { close(entered); <-release })
			}
			return b, err
		}
	})
	t.Cleanup(unblock)
	h.write(t, ".git/HEAD", "ref: refs/heads/original\n")
	var future *Future
	h.call(t, func(scope *cooperative.Scope) error { future = h.s.ensure(scope); return nil })
	<-entered
	if err := h.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := h.s.Reset(scope); err != nil {
			return err
		}
		unblock()
		_, err := future.Await(scope)
		var named interface{ ErrorName() string }
		if !errors.As(err, &named) || named.ErrorName() != "TypeError" || err.Error() != `The "paths[0]" property must be of type string, got object` {
			return errors.New("start null receiver was silently converted to HEAD in CWD")
		}
		if len(h.s.watched) != 0 || h.s.init != nil || h.s.initialized {
			return errors.New("start rejection continued registering or initializing after Reset")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryNullAndPresentEmptyStringAreDistinct(t *testing.T) {
	if nullableDirectory("") != nil {
		t.Fatal("resolved absence lost null representation")
	}
	value := ""
	joined, err := joinNullableDirectory(&value, "refs", "heads", "main")
	if err != nil || joined != filepath.Join("refs", "heads", "main") {
		t.Fatalf("present empty string is valid path input: %q %v", joined, err)
	}
}
