package gitbranch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestOwnedInitialNativeReadinessIsAdmittedBeforeSubsequentOwnerCommand(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	watch := &NativePollWatch{Owner: owner, Stat: os.Stat}
	var stop func()
	defer func() {
		if stop != nil {
			stop()
		}
		watch.Join()
	}()
	events := []string{}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		stop, err = watch.WatchOwned(scope, filepath.Join(t.TempDir(), "missing"), time.Hour, func(next *cooperative.Scope) {
			if !next.BelongsTo(owner) {
				t.Error("listener escaped canonical owner")
			}
			events = append(events, "native-initial")
		})
		if err != nil {
			return err
		}
		if len(events) != 0 {
			return errors.New("native listener ran on registration stack")
		}
		events = append(events, "registered")
		done := make(chan struct{})
		_, err = owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
			events = append(events, "subsequent-command")
			close(done)
			return nil
		})
		if err != nil {
			return err
		}
		return scope.AwaitPromise(done)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"registered", "native-initial", "subsequent-command"}) {
		t.Fatal("already completed native stat admission was scheduler-late", events)
	}
}

func TestOwnedNativeWatchIdentityGuardsDoNotStatOrNotify(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	reads, callbacks := 0, 0
	watch := &NativePollWatch{Owner: owner, Stat: func(p string) (os.FileInfo, error) { reads++; return os.Stat(p) }}
	path := filepath.Join(t.TempDir(), "missing")
	if _, e := watch.WatchOwned(nil, path, time.Hour, func(*cooperative.Scope) { callbacks++ }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := watch.WatchOwned(scope, path, time.Hour, func(*cooperative.Scope) { callbacks++ }); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign watcher admitted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, invalid := range []*NativePollWatch{nil, {}, {Owner: owner}} {
			if _, e := invalid.WatchOwned(scope, path, time.Hour, func(*cooperative.Scope) { callbacks++ }); !errors.Is(e, cooperative.ErrRestricted) {
				return errors.New("incomplete native binding admitted")
			}
		}
		if _, e := watch.WatchOwned(scope, path, time.Hour, nil); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("nil listener admitted")
		}
		return nil
	})
	if err != nil || reads != 0 || callbacks != 0 {
		t.Fatal(err, reads, callbacks)
	}
}

func TestServiceRejectsAmbiguousWatchBindings(t *testing.T) {
	h := newHarness(t, nil)
	options := h.s.options
	options.WatchFileOwned = func(*cooperative.Scope, string, time.Duration, func(*cooperative.Scope)) (func(), error) {
		return func() {}, nil
	}
	_, err := New(options)
	var cap *CapabilityError
	if !errors.As(err, &cap) || cap.Boundary != "multiple watch registration capabilities" {
		t.Fatal(err)
	}
}
