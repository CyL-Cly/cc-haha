package clibootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This fixture keeps the ordinary MainPrint updater, real HTTP client and
// installer minimum-version consumer. It substitutes no updater future/result.
func terminalUpdaterTestMain(t *testing.T, peer string) *MainPrint {
	t.Helper()
	var external atomic.Int32
	guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		external.Add(1)
		t.Error("updater candidate attempted unowned HTTP", r.Method, r.Host)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(func() {
		guard.Close()
		if external.Load() != 0 {
			t.Error("updater candidate escaped its loopback transport", external.Load())
		}
	})
	return backgroundAgentMainWithOptions(t, peer, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC")
		delete(n.Environment, "DISABLE_AUTOUPDATER")
		n.Environment["DISABLE_TELEMETRY"] = "1"
		n.Environment["CC_HAHA_CLI_RELEASE_API"] = peer
		n.Environment["HTTPS_PROXY"] = guard.URL
		config := n.Environment["CLAUDE_CONFIG_DIR"]
		if err := os.MkdirAll(filepath.Join(config, "cache"), 0700); err != nil {
			t.Fatal(err)
		}
		for path, data := range map[string]string{
			filepath.Join(config, "settings.json"):         `{"enabledPlugins":{"sample@mkt":false},"autoUpdatesChannel":"stable","minimumVersion":"2.3.4","futureUpdaterCandidate":17}`,
			filepath.Join(config, ".claude.json"):          `{"lastReleaseNotesSeen":"2.3.4","futureUpdaterCandidate":17}`,
			filepath.Join(config, "cache", "changelog.md"): "## 0.0.0\n",
		} {
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}, func(options *MainPrintOptions) {
		options.Interactive = true
		options.Version = "2.3.4"
	})
}

func terminalUpdaterTestCatalog(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`[{"tag_name":"v2.3.3","assets":[{"name":"claude-haha-cli-manifest.json","browser_download_url":"http://127.0.0.1:1/not-fetched-below-minimum"}]}]`))
}

func waitTerminalUpdaterTest(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal(label)
	}
}

func TestTerminalUpdaterHeldHTTPDoesNotRetainNestedOwnerAwait(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	var enteredOnce, cancelledOnce sync.Once
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/releases" {
			t.Error("unexpected actual updater request", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		requests.Add(1)
		enteredOnce.Do(func() { close(entered) })
		<-r.Context().Done()
		cancelledOnce.Do(func() { close(cancelled) })
	}))
	t.Cleanup(peer.Close)
	m := terminalUpdaterTestMain(t, peer.URL)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancelCause := errors.New("candidate physical updater cancel cause")
	gate, outerEntered := make(chan struct{}), make(chan struct{})
	var gateOnce sync.Once
	openGate := func() { gateOnce.Do(func() { close(gate) }) }
	var outerContinued bool
	outer, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		close(outerEntered)
		if err := scope.AwaitPromise(gate); err != nil {
			return err
		}
		outerContinued = true
		return nil
	})
	if err != nil {
		cancel(cancelCause)
		t.Fatal(err)
	}
	var updateDone chan error
	var updateErr error
	updateJoined := false
	joinUpdate := func() {
		if updateDone == nil || updateJoined {
			return
		}
		select {
		case updateErr = <-updateDone:
			updateJoined = true
		case <-time.After(5 * time.Second):
			t.Error("actual updater failed to join cancelled physical work")
		}
	}
	defer func() {
		cancel(cancelCause)
		openGate()
		joinUpdate()
		select {
		case <-outer.Done():
			if err := outer.Wait(); err != nil {
				t.Error("outer Owner receipt", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("outer Owner receipt did not join")
		}
	}()
	waitTerminalUpdaterTest(t, outerEntered, "outer Owner wait did not start")
	updateDone = make(chan error, 1)
	go func() { updateDone <- m.CheckTerminalUpdate(ctx, nil) }()
	waitTerminalUpdaterTest(t, entered, "physical loopback updater request did not start")
	type observerSnapshot struct {
		resumed    bool
		gateClosed bool
		stack      string
	}
	type observerAdmission struct {
		receipt *cooperative.Receipt
		err     error
	}
	observed := make(chan observerSnapshot, 1)
	admitted := make(chan observerAdmission, 1)
	gateReceipt, err := m.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
		openGate()
		// Admission is causally after closure in this same Owner callback.
		// Record this causal edge independently of queue-priority reasoning.
		observer, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
			closed := false
			select {
			case <-gate:
				closed = true
			default:
			}
			buffer := make([]byte, 128<<10)
			size := runtime.Stack(buffer, false)
			observed <- observerSnapshot{resumed: outerContinued, gateClosed: closed, stack: string(buffer[:size])}
			return nil
		})
		admitted <- observerAdmission{receipt: observer, err: err}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var admission observerAdmission
	select {
	case admission = <-admitted:
	case <-time.After(5 * time.Second):
		t.Fatal("gate callback did not publish its observer admission")
	}
	if admission.err != nil {
		t.Fatal(admission.err)
	}
	observer := admission.receipt
	waitTerminalUpdaterTest(t, observer.Done(), "Owner observer did not run while real HTTP was held")
	if err := observer.Wait(); err != nil {
		t.Fatal(err)
	}
	observation := <-observed
	if !observation.gateClosed {
		t.Fatal("Owner observer ran before the actual gate was closed")
	}
	resumedBeforeHTTP := observation.resumed
	waitTerminalUpdaterTest(t, gateReceipt.Done(), "gate microtask did not finish")
	if err := gateReceipt.Wait(); err != nil {
		t.Fatal(err)
	}
	state, err := m.TerminalUpdateState(context.Background())
	if err != nil || string(state.Get("running")) != "true" {
		t.Fatal("real held update lost running state", state, err)
	}
	if err := m.CheckTerminalUpdate(context.Background(), nil); err != nil {
		t.Fatal("duplicate held update changed error", err)
	}
	if requests.Load() != 1 {
		t.Fatal("duplicate started another real HTTP request", requests.Load())
	}
	cancel(cancelCause)
	waitTerminalUpdaterTest(t, cancelled, "physical request did not observe terminal cancellation")
	joinUpdate()
	if !updateJoined || updateErr != context.Canceled {
		t.Fatal("original cancellation error identity/join changed", updateErr, context.Cause(ctx))
	}
	state, err = m.TerminalUpdateState(context.Background())
	if err != nil || string(state.Get("running")) != "false" {
		t.Fatal("completion left running state after cancellation", state, err)
	}
	if !resumedBeforeHTTP {
		t.Fatalf("outer Owner continuation remained blocked after its gate settled while actual updater HTTP was held\nOwner observer stack:\n%s", observation.stack)
	}
}

func TestTerminalUpdaterErrorsAndEarlyCancellationRetainIdentity(t *testing.T) {
	for _, profile := range []string{"cancel-before-admission", "checking-emit-error", "completion-emit-error", "cancel-during-checking", "cancel-and-error-during-completion", "physical-failure"} {
		t.Run(profile, func(t *testing.T) {
			var requests atomic.Int32
			var failPhysical atomic.Bool
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusOK)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/releases" {
					t.Error("unexpected actual updater endpoint", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				requests.Add(1)
				if failPhysical.Load() {
					http.Error(w, "owned candidate failure", http.StatusServiceUnavailable)
					return
				}
				terminalUpdaterTestCatalog(w)
			}))
			t.Cleanup(peer.Close)
			m := terminalUpdaterTestMain(t, peer.URL)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			cause := errors.New("owned candidate cancellation cause")
			emitFailure := errors.New("owned candidate exact emit error")
			if profile == "cancel-before-admission" {
				cancel(cause)
				if err := m.CheckTerminalUpdate(ctx, nil); err != cause || requests.Load() != 0 {
					t.Fatal("early admission cancellation identity/physical work changed", err, requests.Load())
				}
				return
			}
			if err := m.CheckTerminalUpdate(context.Background(), nil); err != nil || requests.Load() != 1 {
				t.Fatal("real minimum-version warm check", err, requests.Load())
			}
			before, err := m.TerminalUpdateState(context.Background())
			if err != nil || before.String("latest") != "2.3.3" || string(before.Get("running")) != "false" {
				t.Fatal("actual warm state", before, err)
			}
			if profile == "physical-failure" {
				failPhysical.Store(true)
			}
			var checking, completed int
			emit := func(value protocol.JSONObject) error {
				if value.String("phase") == "checking" {
					checking++
					switch profile {
					case "checking-emit-error":
						return emitFailure
					case "cancel-during-checking":
						cancel(cause)
					}
					return nil
				}
				completed++
				switch profile {
				case "completion-emit-error":
					return emitFailure
				case "cancel-and-error-during-completion":
					cancel(cause)
					return emitFailure
				case "physical-failure":
					if value.String("phase") != "install_failed" || value.String("error") != "CLI release request failed: HTTP 503" {
						t.Error("physical failure was not retained as the actual snapshot", value)
					}
				}
				return nil
			}
			err = m.CheckTerminalUpdate(ctx, emit)
			wantErr, wantRequests, wantCompleted := error(nil), int32(2), 1
			switch profile {
			case "checking-emit-error":
				wantErr, wantRequests, wantCompleted = emitFailure, 1, 0
			case "completion-emit-error", "cancel-and-error-during-completion":
				wantErr = emitFailure
			case "cancel-during-checking":
				wantErr, wantRequests, wantCompleted = context.Canceled, 1, 0
			}
			if err != wantErr || requests.Load() != wantRequests || checking != 1 || completed != wantCompleted {
				t.Fatal("original error identity/physical dispatch/completion changed", err, wantErr, requests.Load(), checking, completed)
			}
			after, err := m.TerminalUpdateState(context.Background())
			if err != nil || string(after.Get("running")) != "false" || after.String("latest") != "2.3.3" {
				t.Fatal("actual completion state lost", after, err)
			}
			failPhysical.Store(false)
			if err := m.CheckTerminalUpdate(context.Background(), nil); err != nil || requests.Load() != wantRequests+1 {
				t.Fatal("early/completion failure left stale running ref", err, requests.Load())
			}
		})
	}
}
