package connectors

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func serviceFixture(t *testing.T, adapter Adapter, io *controlledIO, write func(string, []byte) error, source string) *Service {
	t.Helper()
	root := t.TempDir()
	if source != "" {
		seedState(t, root, source)
	}
	def := connectorDef("cli", "shared")
	if adapter == nil {
		adapter = io
	}
	s, err := New(Options{Root: root, Platform: "darwin-arm64", Definitions: []protocol.JSONObject{def}, CreateAdapter: func(d protocol.JSONObject, _ string) (Adapter, error) {
		return adapter, io.step("factory", d.String("version"))
	}, Bridge: controlledBridge{io}, Now: func() time.Time { return time.Unix(0, 0) }, UUID: func() string { return "operation" }, WriteState: write})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}

func waitConnector(t *testing.T, s *Service) protocol.JSONObject {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Wait(ctx, "sample"); err != nil {
		t.Fatal(err)
	}
	v, err := s.Get("sample")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type gatedAuthorization struct {
	*controlledIO
	entered, release chan struct{}
}

func (a *gatedAuthorization) Authenticate(ctx context.Context, _ Installation, p Progress) error {
	url := "https://accounts.feishu.cn/login"
	p("awaiting-authorization", &url)
	close(a.entered)
	<-a.release
	return ctx.Err()
}

func TestCancelRetainsOwnershipUntilAdapterExits(t *testing.T) {
	io := &controlledIO{def: connectorDef("cli", "shared")}
	adapter := &gatedAuthorization{io, make(chan struct{}), make(chan struct{})}
	s := serviceFixture(t, adapter, io, nil, "")
	if _, err := s.Action("sample", "prepare", ActionOptions{}); err != nil {
		t.Fatal(err)
	}
	waitConnector(t, s)
	if _, err := s.Action("sample", "authenticate", ActionOptions{AcknowledgeSharedCredentials: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-adapter.entered:
	case <-time.After(time.Second):
		t.Fatal("authorization not entered")
	}
	v, err := s.Action("sample", "cancel", ActionOptions{})
	if err != nil || nested(v.Get("operation")).String("phase") != "cancelling" || nested(v.Get("operation")).Get("authUrl") != nil {
		t.Fatalf("cancel view: %s %v", raw(v), err)
	}
	_, err = s.Action("sample", "check", ActionOptions{AcknowledgeSharedCredentials: true})
	var failure *Error
	if !errors.As(err, &failure) || failure.Status != 409 {
		t.Fatalf("active operation lock lost: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := s.Wait(ctx, "sample"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ownership released before adapter completion: %v", err)
	}
	cancel()
	close(adapter.release)
	v = waitConnector(t, s)
	if v.String("status") != "needs-auth" || v.String("runtime") != "missing" || truthy(v.Get("enabled")) || v.Get("error") != nil || v.Get("failedPhase") != nil {
		t.Fatalf("cancel must return neutral disconnected runtime: %s", raw(v))
	}
}

type prefixContext struct {
	context.Context
	checked, release chan struct{}
	once             sync.Once
}

func (c *prefixContext) Err() error {
	first := false
	c.once.Do(func() { first = true; close(c.checked); <-c.release })
	if first {
		return nil
	}
	return c.Context.Err()
}

func TestProgressRechecksCancellationInsideOperationLock(t *testing.T) {
	io := &controlledIO{def: connectorDef("cli", "shared")}
	s := serviceFixture(t, nil, io, nil, "")
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &prefixContext{Context: parent, checked: make(chan struct{}), release: make(chan struct{})}
	op := &operation{cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.operations["sample"] = op
	if err := s.patchLocked("sample", record(field("operation", record(field("phase", "cancelling"))))); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() { defer close(finished); s.progress("sample", ctx, op)("awaiting-authorization", nil) }()
	<-ctx.checked
	cancel()
	close(ctx.release)
	s.mu.Unlock()
	<-finished
	v, _ := s.Get("sample")
	if nested(v.Get("operation")).String("phase") != "cancelling" {
		t.Fatalf("late progress overwrote cancellation: %s", raw(v))
	}
	s.mu.Lock()
	delete(s.operations, "sample")
	s.mu.Unlock()
	close(op.done)
}

func TestConstructorFailureDoesNotDetachBeforeOriginalAsyncBoundary(t *testing.T) {
	io := &controlledIO{def: connectorDef("cli", "shared"), behavior: record(field("fail", "factory"))}
	s := serviceFixture(t, nil, io, nil, `{"connectors":{"sample":{"installed":true,"enabled":true,"installedVersion":"1.0.0","installation":{"directory":"/owned/connector","command":"owned-cli","args":[],"env":{}}}}}`)
	v, err := s.Action("sample", "authenticate", ActionOptions{AcknowledgeSharedCredentials: true})
	if err != nil || string(v.Get("enabled")) != "true" || v.String("status") != "authorizing" {
		t.Fatalf("constructor failure changed original admission projection: %s %v", raw(v), err)
	}
	v = waitConnector(t, s)
	if v.String("status") != "error" || truthy(v.Get("enabled")) {
		t.Fatalf("failed constructor must end disabled: %s", raw(v))
	}
}

func TestCatchPersistenceFailureStillDisablesThroughOuterCleanup(t *testing.T) {
	io := &controlledIO{def: connectorDef("cli", "shared"), authenticated: true, behavior: record(field("ready", false))}
	failed := false
	write := func(path string, source []byte) error {
		v := nested(source)
		state := nested(nested(v.Get("connectors")).Get("sample"))
		if !failed && state.String("status") == "error" {
			failed = true
			return errors.New("disk unavailable")
		}
		return writeStateAtomic(path, source)
	}
	s := serviceFixture(t, nil, io, write, `{"connectors":{"sample":{"installed":true,"enabled":true,"installedVersion":"1.0.0","installation":{"directory":"/owned/connector","command":"owned-cli","args":[],"env":{}}}}}`)
	if _, err := s.Action("sample", "check", ActionOptions{AcknowledgeSharedCredentials: true}); err != nil {
		t.Fatal(err)
	}
	waitConnector(t, s)
	if !failed {
		t.Fatal("storage failure point not exercised")
	}
	disabled := 0
	reloads := 0
	for _, call := range io.calls {
		if call[0] == "enabled" && call[1] == false {
			disabled++
		}
		if call[0] == "reload" {
			reloads++
		}
	}
	if disabled != 3 || reloads != 4 {
		t.Fatalf("outer cleanup missing after catch storage failure: calls=%v", io.calls)
	}
}

func TestGetComparesOriginalDecodedVersionStrings(t *testing.T) {
	io := &controlledIO{def: connectorDef("cli", "shared")}
	s := serviceFixture(t, nil, io, nil, `{"connectors":{"sample":{"installed":true,"installedVersion":"1\u002e0.0"}}}`)
	v, err := s.Get("sample")
	if err != nil || truthy(v.Get("updateAvailable")) {
		t.Fatalf("equal string values advertised update: %s %v", raw(v), err)
	}
	if filepath.Base(s.store.path) != "state.json" {
		t.Fatal("original state path changed")
	}
}
