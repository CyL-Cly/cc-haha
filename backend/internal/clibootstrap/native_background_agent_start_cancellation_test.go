package clibootstrap

// Regress cancellation of actual startup registration on the same Owner and
// across a Detached child. Business observations follow the original native
// ledger drain and precede explicitly labelled fixture-only retirement.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

type startupCancellationWitness struct {
	mu   sync.Mutex
	id   string
	join func(context.Context) error
	err  error
}

func (w *startupCancellationWitness) capture(root *MainPrint, scope *cooperative.Scope, id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.id != "" {
		return false
	}
	w.id = id
	entry, err := root.Controller.BackgroundTaskOnOwner(scope, id)
	w.err = err
	if entry != nil {
		w.join = entry.Join
	}
	return true
}

func (w *startupCancellationWitness) snapshot() (string, func(context.Context) error, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.id, w.join, w.err
}

func startupCancellationShutdown(t *testing.T, m *MainPrint) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- m.ShutdownSession(ctx) }()
	select {
	case err := <-returned:
		if err != nil {
			t.Errorf("bounded owned fixture ShutdownSession: %v", err)
		}
	case <-ctx.Done():
		t.Errorf("owned fixture ShutdownSession did not return within its bound: %v", context.Cause(ctx))
	}
}

func startupCancellationRoot(t *testing.T, output func([]byte) error) (*MainPrint, *atomic.Int32) {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"maxConcurrentSubagents":1,"enabledPlugins":{"sample@mkt":false}}`)
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests.Add(1)
		// Return promptly even if broken startup unexpectedly starts a query;
		// the zero-request assertion still reports that business failure.
		emitPlanFixture(w, 1, "", "UNEXPECTED_CANCELLED_START_QUERY")
	}))
	t.Cleanup(peer.Close)
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-cancellation-loopback-fake-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual cancelled startup fixture", Tools: []string{"Agent,Read"}, MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: output})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { startupCancellationShutdown(t, m) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	return m, &requests
}

func startupCancellationRetireRejectedFixture(t *testing.T, root *MainPrint, witness *startupCancellationWitness) {
	t.Helper()
	id, _, _ := witness.snapshot()
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := root.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry, err := root.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil || entry == nil {
			return err
		}
		t.Logf("fixture-only cleanup after cancelled-start business observations: cancel/delete %s (status %s)", id, entry.Data.String("status"))
		entry.Cancel(context.Canceled)
		return root.Controller.DeleteBackgroundTaskOnOwner(scope, id)
	}); err != nil {
		t.Errorf("rejected fixture task retirement: %v", err)
	}
}

func startupCancellationAssertSettled(t *testing.T, root *MainPrint, parentCtx context.Context, witness *startupCancellationWitness, name string, requests *atomic.Int32) {
	t.Helper()
	id, join, captureErr := witness.snapshot()
	if id == "" || join == nil || captureErr != nil {
		t.Fatalf("actual registered consumer identity/Join was not captured: id=%q Join=%v error=%v", id, join != nil, captureErr)
	}
	joinCtx, cancelJoin := context.WithTimeout(parentCtx, 100*time.Millisecond)
	joinErr := join(joinCtx)
	cancelJoin()
	if joinErr != nil {
		t.Errorf("cancelled untransferred startup left its actual Join open: %v", joinErr)
	}
	if err := root.Owner.Run(parentCtx, func(scope *cooperative.Scope) error {
		entry, err := root.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			return err
		}
		if entry != nil {
			t.Errorf("cancelled startup leaked actual root task: %s", rawMain(entry.Data))
		}
		resolved, err := root.Controller.ResolveAgentNameOnOwner(scope, name)
		if err != nil {
			return err
		}
		if resolved != "" {
			t.Errorf("cancelled startup leaked actual root name: name=%s id=%s", name, resolved)
		}
		return nil
	}); err != nil {
		t.Errorf("uncancelled parent could not observe cancelled startup terminal state: %v", err)
	}
	if requests.Load() != 0 {
		t.Errorf("cancelled pre-transfer startup opened %d actual loopback model requests", requests.Load())
	}
}

func TestActualBackgroundAgentCancelledStartScopeCompensatesOnSameOwner(t *testing.T) {
	parentCtx, parentCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer parentCancel()
	startCtx, startCancel := context.WithCancelCause(parentCtx)
	defer startCancel(context.Canceled)
	cause := errors.New("owned same-owner start cancelled at task_started")
	var root *MainPrint
	var publishingScope *cooperative.Scope
	witness := &startupCancellationWitness{}
	output := func(data []byte) error {
		var event protocol.JSONObject
		if err := event.UnmarshalJSON(data); err != nil {
			return err
		}
		if event.String("type") == "system" && event.String("subtype") == "task_started" && event.String("task_type") == "local_agent" {
			if witness.capture(root, publishingScope, event.String("task_id")) {
				startCancel(cause)
			}
		}
		// Successful publication is retained. Cancellation is discovered by
		// the original following native lifetime Acquire, not a fake hook.
		return nil
	}
	var requests *atomic.Int32
	root, requests = startupCancellationRoot(t, output)
	t.Cleanup(func() { startupCancellationRetireRejectedFixture(t, root, witness) })
	const name = "same-owner-cancelled-start"
	var startErr error
	runErr := root.Owner.Run(startCtx, func(scope *cooperative.Scope) error {
		request, err := actualBackgroundAgentRequest(scope, root, "This cancelled child must not reach a provider")
		if err != nil {
			return err
		}
		request.Name, request.Description = name, name
		publishingScope = scope
		defer func() { publishingScope = nil }()
		_, startErr = root.runBackgroundAgent(startCtx, scope, request)
		return nil
	})
	if runErr != nil && !errors.Is(runErr, cause) {
		t.Fatal("actual same-owner start failed before its cancellation window", runErr)
	}
	if !errors.Is(startErr, cause) {
		t.Errorf("actual start lost the caller cancellation cause: %v", startErr)
	}
	// This is an existing non-closing ledger checkpoint. It joins original
	// admitted native deliveries without changing task/name business state.
	if err := root.Host.WaitTranscriptRuntime(parentCtx); err != nil {
		t.Errorf("same-owner native startup custody did not drain: %v", err)
	}
	startupCancellationAssertSettled(t, root, parentCtx, witness, name, requests)
}

func TestActualDetachedBackgroundAgentCancelledRegistrationRetainsRootCompensationCustody(t *testing.T) {
	parentCtx, parentCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer parentCancel()
	root, requests := startupCancellationRoot(t, nil)
	var childOptions MainPrintOptions
	if err := root.Owner.Run(parentCtx, func(scope *cooperative.Scope) error {
		request, err := actualBackgroundAgentRequest(scope, root, "actual detached cancellation caller")
		if err != nil {
			return err
		}
		childOptions, err = root.nativeIndependentAgentOptions(scope, request, "a0123456789abcde1", root.workingDirectory(), "fixture-model", root.options.Native.Temp, request.SystemPromptSections, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	child, err := NewMainPrint(childOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { startupCancellationShutdown(t, child) })
	witness := &startupCancellationWitness{}
	// This cleanup runs before child/root ShutdownSession. On old code it
	// retires only the real rejected ID after all RED business observations.
	t.Cleanup(func() { startupCancellationRetireRejectedFixture(t, root, witness) })
	if err := child.StartSession(parentCtx); err != nil {
		t.Fatal(err)
	}
	startCtx, startCancel := context.WithCancelCause(parentCtx)
	defer startCancel(context.Canceled)
	cause := errors.New("owned detached caller cancelled after actual root registration")
	const name = "detached-cancelled-start"
	observerStop := make(chan struct{})
	observerReady := make(chan struct{})
	observerDone := make(chan struct{})
	var stopOnce sync.Once
	var observerErr error
	// Keep a genuine root Owner scope alive while it pumps independent root
	// registrations. The publication callback has no Scope parameter, so it
	// must not retain a scope whose owning Run has already returned.
	go func() {
		defer close(observerDone)
		observerErr = root.Owner.Run(parentCtx, func(rootScope *cooperative.Scope) error {
			_, unsubscribe, err := root.Controller.SubscribeBackgroundTasksOnOwner(rootScope, func(snapshot protocol.JSONObject) {
				data, err := snapshot.MarshalJSON()
				if err != nil {
					return
				}
				var tasks map[string]protocol.JSONObject
				if json.Unmarshal(data, &tasks) != nil {
					return
				}
				for id, task := range tasks {
					if task.String("type") == "local_agent" && task.String("description") == name && task.String("ownerAgentId") == child.options.AgentID {
						if witness.capture(root, rootScope, id) {
							startCancel(cause)
						}
					}
				}
			})
			close(observerReady)
			if err != nil {
				return err
			}
			defer func() {
				if err := unsubscribe(rootScope); err != nil {
					t.Errorf("actual root publication observer unsubscribe: %v", err)
				}
			}()
			return rootScope.AwaitPromise(observerStop)
		})
	}()
	stopObserver := func(ctx context.Context) error {
		stopOnce.Do(func() { close(observerStop) })
		select {
		case <-observerDone:
			return observerErr
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := stopObserver(ctx); err != nil {
			t.Errorf("root publication observer did not join: %v", err)
		}
	})
	select {
	case <-observerReady:
	case <-observerDone:
		t.Fatal("actual root observer failed before readiness", observerErr)
	case <-parentCtx.Done():
		t.Fatal("actual root observer never became ready", context.Cause(parentCtx))
	}
	var startErr error
	runErr := child.Owner.Run(startCtx, func(scope *cooperative.Scope) error {
		request, err := actualBackgroundAgentRequest(scope, child, "This cancelled nested child must not reach a provider")
		if err != nil {
			return err
		}
		request.Name, request.Description = name, name
		_, startErr = child.runBackgroundAgent(startCtx, scope, request)
		return nil
	})
	if runErr != nil && !errors.Is(runErr, cause) {
		t.Error("actual detached start failed before root publication cancellation", runErr)
	}
	if !errors.Is(startErr, cause) {
		t.Errorf("actual detached start lost its cancellation cause: %v", startErr)
	}
	// Do not stop the root pump or shut down either MainPrint first: the
	// original caller ledger must hold target compensation through its real
	// root receipt. Old code may drain this ledger yet leave a leaked task.
	if err := child.Host.WaitTranscriptRuntime(parentCtx); err != nil {
		t.Errorf("detached registration native custody did not drain: %v", err)
	}
	if err := stopObserver(parentCtx); err != nil {
		t.Errorf("root observer did not naturally finish before business postconditions: %v", err)
	}
	startupCancellationAssertSettled(t, root, parentCtx, witness, name, requests)
}
