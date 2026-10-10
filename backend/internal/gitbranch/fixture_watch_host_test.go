package gitbranch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// This host keeps its event promise on the Service's existing Owner. Readiness
// on a polling worker is not the callback-end that resolves Source's event.
type fixtureWatchHost struct {
	service        *Service
	headPath       string
	armed          *Future
	firstCallbacks map[string]bool
	firstWaiters   map[string]*Future
}

func (h *fixtureWatchHost) arm(scope *cooperative.Scope) (*Future, error) {
	if err := h.service.check(scope); err != nil {
		return nil, err
	}
	if h.armed != nil {
		return nil, &CapabilityError{"overlapping fixture HEAD event promises"}
	}
	h.armed = h.service.future()
	return h.armed, nil
}

func (h *fixtureWatchHost) callbackEnd(scope *cooperative.Scope, path string) {
	if h.firstCallbacks == nil {
		h.firstCallbacks = map[string]bool{}
	}
	h.firstCallbacks[path] = true
	if future := h.firstWaiters[path]; future != nil {
		h.service.settle(scope, future, result{raw: raw(nil)})
	}

	if path == h.headPath && h.armed != nil {
		h.service.settle(scope, h.armed, result{raw: raw(nil)})
	}
}

// firstCallback observes only a corresponding native missing-loose-ref event.
// It neither joins pending reads nor drains arbitrary owner work.
func (h *fixtureWatchHost) firstCallback(scope *cooperative.Scope, path string) error {
	if err := h.service.check(scope); err != nil {
		return err
	}
	prefix := filepath.Join(filepath.Dir(h.headPath), "refs", "heads") + string(filepath.Separator)
	if path == h.headPath || !strings.HasPrefix(filepath.Clean(path), prefix) {
		return &CapabilityError{"fixture expected exact missing loose-ref callback path"}
	}
	if h.firstCallbacks[path] {
		return nil
	}
	if h.firstWaiters == nil {
		h.firstWaiters = map[string]*Future{}
	}
	future := h.firstWaiters[path]
	if future == nil {
		future = h.service.future()
		h.firstWaiters[path] = future
	}
	_, err := future.Await(scope)
	return err
}

// immediate is the pinned Bun native ImmediateObject batch capability.
// A nil binding rejects before arming or writing. The extra path receipts
// declare only this controlled caller's first missing-ref callbacks.
func (h *fixtureWatchHost) change(owner *cooperative.Owner, immediate func(*cooperative.Scope) error, write func() error, missingRefs ...string) error {
	if immediate == nil {
		return &CapabilityError{"actual setImmediate/check-phase host binding is not migrated"}
	}
	ctx := context.Background()
	cancel := func() {}
	if len(missingRefs) != 0 {
		if len(missingRefs) != 2 {
			return &CapabilityError{"fixture requires previous and next missing loose-ref paths"}
		}
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
	}
	defer cancel()
	return owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := immediate(scope); err != nil {
			return err
		}
		if len(missingRefs) != 0 {
			if err := h.firstCallback(scope, missingRefs[0]); err != nil {
				return err
			}
		}
		event, err := h.arm(scope)
		if err != nil {
			return err
		}
		defer func() { h.armed = nil }()
		if err := write(); err != nil {
			return err
		}
		if _, err := event.Await(scope); err != nil {
			return err
		}
		if err := immediate(scope); err != nil {
			return err
		}
		if len(missingRefs) != 0 {
			return h.firstCallback(scope, missingRefs[1])
		}
		return nil
	})
}

func newActualWatchService(t *testing.T, root string, observe func(*cooperative.Scope, string)) (*Service, *cooperative.Owner, *NativePollWatch) {
	t.Helper()
	owner := cooperative.New()
	watch := &NativePollWatch{Stat: os.Stat}
	service, err := New(Options{Owner: owner, CWD: func() string { return root }, Now: func() int64 { return time.Now().UnixMilli() }, StatSync: os.Stat, Stat: os.Stat, ReadFile: os.ReadFile, WatchFile: watch.Watch, ScrollDraining: func() bool { return false }, RegisterCleanup: func(func(*cooperative.Scope) error) {}, NodeEnv: "test", ObserveWatchCallbackEnd: observe})
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()); watch.Join(); owner.Close() })
	return service, owner, watch
}

func TestActualInitialMissingCallbackEndUsesExistingOwnerAfterRegistration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "HEAD")
	var service *Service
	var owner *cooperative.Owner
	var event *Future
	var callbackErr error
	callbacks := 0
	registrationReturned := false
	service, owner, _ = newActualWatchService(t, root, func(scope *cooperative.Scope, observed string) {
		callbacks++
		if !scope.BelongsTo(owner) || !registrationReturned || observed != path || !service.branch.dirty {
			callbackErr = errors.New("initial callback-end escaped existing Owner or preceded actual dirty/registration boundary")
		}
		service.settle(scope, event, result{raw: raw(nil)})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		service.branch = &cacheEntry{value: raw("main")}
		event = service.future()
		if err := service.watch(scope, path, false); err != nil {
			return err
		}
		registrationReturned = true
		if callbacks != 0 {
			return errors.New("initial visible callback executed on registration stack")
		}
		if _, err := event.Await(scope); err != nil {
			return err
		}
		if callbacks != 1 {
			return errors.New("initial missing callback-end was not exactly once")
		}
		return callbackErr
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestActualConfigCallbackCannotCompleteExactHEADGate(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0700); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(gitDir, "HEAD")
	config := filepath.Join(gitDir, "config")
	for p, text := range map[string]string{head: "ref: refs/heads/main\n", config: "first", filepath.Join(gitDir, "refs", "heads", "main"): "one", filepath.Join(gitDir, "refs", "heads", "changed"): "two"} {
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	host := &fixtureWatchHost{headPath: head}
	var service *Service
	var owner *cooperative.Owner
	var configEvent *Future
	var callbackErr error
	var paths []string
	service, owner, _ = newActualWatchService(t, root, func(scope *cooperative.Scope, path string) {
		paths = append(paths, path)
		if !scope.BelongsTo(owner) || !service.branch.dirty {
			callbackErr = errors.New("watch observer preceded actual dirty reduction or escaped Owner")
		}
		host.callbackEnd(scope, path)
		if path == config {
			service.settle(scope, configEvent, result{raw: raw(nil)})
		}
	})
	host.service = service
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		service.gitDir, service.commonDir = &gitDir, &gitDir
		service.branch = &cacheEntry{value: raw("main")}
		configEvent = service.future()
		headEvent, err := host.arm(scope)
		if err != nil {
			return err
		}
		defer func() { host.armed = nil }()
		if err := service.watch(scope, config, false); err != nil {
			return err
		}
		if err := service.watch(scope, head, true); err != nil {
			return err
		}
		if err := os.WriteFile(config, []byte("a different config length"), 0600); err != nil {
			return err
		}
		if _, err := configEvent.Await(scope); err != nil {
			return err
		}
		select {
		case <-headEvent.Done():
			return errors.New("non-HEAD callback completed exact HEAD gate")
		default:
		}
		if len(paths) != 1 || paths[0] != config {
			return errors.New("actual config callback was not the first completed callback")
		}
		service.branch.dirty = false
		if err := os.WriteFile(head, []byte("ref: refs/heads/changed\n"), 0600); err != nil {
			return err
		}
		if _, err := headEvent.Await(scope); err != nil {
			return err
		}
		if len(paths) < 2 || paths[1] != head {
			return errors.New("exact HEAD callback-end was not observed")
		}
		return callbackErr
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFixtureWatchChangeRefusesMissingImmediateBeforeWriteOrArm(t *testing.T) {
	root := t.TempDir()
	service, owner, _ := newActualWatchService(t, root, nil)
	host := &fixtureWatchHost{service: service, headPath: filepath.Join(root, "HEAD")}
	writes := 0
	err := host.change(owner, nil, func() error { writes++; return os.WriteFile(host.headPath, []byte("changed"), 0600) })
	var capability *CapabilityError
	if !errors.As(err, &capability) || capability.Boundary != "actual setImmediate/check-phase host binding is not migrated" {
		t.Fatalf("missing real native binding must remain typed capability: %v", err)
	}
	if writes != 0 || host.armed != nil {
		t.Fatal("unproved check-phase changed file or armed a fake event")
	}
	if _, err := os.Stat(host.headPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused file unexpectedly exists: %v", err)
	}
}

func TestExactFirstMissingRefReceiptRejectsUnrelatedNativeConfigAndForeignOwner(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0700); err != nil {
		t.Fatal(err)
	}
	head, config, ref := filepath.Join(gitDir, "HEAD"), filepath.Join(gitDir, "config"), filepath.Join(gitDir, "refs", "heads", "expected")
	for path, data := range map[string]string{head: "ref: refs/heads/expected", config: "before"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	host := &fixtureWatchHost{headPath: head}
	var configEvent *Future
	service, owner, _ := newActualWatchService(t, root, func(scope *cooperative.Scope, path string) {
		host.callbackEnd(scope, path)
		if path == config {
			service := host.service
			service.settle(scope, configEvent, result{raw: raw(nil)})
		}
	})
	host.service = service
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		configEvent = service.future()
		refEvent := service.future()
		host.firstWaiters = map[string]*Future{ref: refEvent}
		if err := host.firstCallback(scope, head); err == nil {
			return errors.New("HEAD accepted as missing loose-ref")
		}
		if err := service.watch(scope, config, false); err != nil {
			return err
		}
		if err := os.WriteFile(config, []byte("a different length"), 0600); err != nil {
			return err
		}
		if _, err := configEvent.Await(scope); err != nil {
			return err
		}
		select {
		case <-refEvent.Done():
			return errors.New("config callback settled missing-ref receipt")
		default:
		}
		if host.firstCallbacks[ref] {
			return errors.New("missing ref inferred from another callback")
		}
		if err := service.watch(scope, ref, false); err != nil {
			return err
		}
		if err := host.firstCallback(scope, ref); err != nil {
			return err
		}
		if !host.firstCallbacks[ref] {
			return errors.New("actual missing-ref callback not observed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if err := host.firstCallback(scope, ref); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("cached receipt accepted foreign owner")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
