package sdkhost_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

// Every case starts from the frozen original on-disk files, real activation,
// real config resolver, Factory, writer and private physical TLS transport.
// Controls observe or inject an external failure; they never resolve query,
// accepted messages, prepared body, usage or SDK results.
type originalLoopbackWire struct{ Prefix, FirstTail, SecondWire string }

func originalBoundary(t *testing.T, control nativeControls) ([]byte, map[string]any) {
	t.Helper()
	return originalBoundaryInHome(t, control, t.TempDir())
}

func originalBoundaryInHome(t *testing.T, control nativeControls, directory string) ([]byte, map[string]any) {
	t.Helper()
	data, e := os.ReadFile("testdata/source_contract.json")
	must(e)
	var items []frozenCase
	must(json.Unmarshal(data, &items))
	item := items[0]
	if control.ExternalWire != nil {
		// Immutable external SSE bytes, not prepared requests/consumer results.
		item.Wires = *control.ExternalWire
	}
	home, e := filepath.EvalSymlinks(directory)
	must(e)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("BUN_OPTIONS", "--no-env-file")
	t.Setenv("CC_HAHA_FIXTURE_TEMP_ROOT", os.TempDir())
	t.Setenv("CC_HAHA_SESSION_CONTROLLER_FIXTURE", "1")
	t.Setenv("NODE_ENV", "production")
	t.Setenv("TZ", "UTC")
	t.Chdir(home)
	project := projectpath.Sanitize(home)
	replace := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "<home>", home), "<project>", project)
	}
	for path, value := range item.Files {
		path = replace(path)
		must(os.MkdirAll(filepath.Dir(path), 0700))
		must(os.WriteFile(path, []byte(replace(value)), 0600))
		must(os.Chtimes(path, time.UnixMilli(1700000000000), time.UnixMilli(1700000000000)))
	}
	ready, release := make(chan struct{}), make(chan struct{})
	var onceReady, onceRelease sync.Once
	var count int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__fixture/first-user-persisted":
			onceReady.Do(func() { close(ready) })
			_, _ = w.Write([]byte("OK"))
			return
		case "/__fixture/release-final":
			onceRelease.Do(func() { close(release) })
			_, _ = w.Write([]byte("OK"))
			return
		}
		count++
		w.Header().Set("Content-Type", "text/event-stream")
		if control.ExternalWire != nil {
			// This raw terminal response has no content_block_stop to release
			// the older persistence fixture's split tail. Send the declared
			// external response on physical POST, without an inferred phase.
			if count == 1 {
				_, _ = w.Write([]byte(item.Wires.Prefix + item.Wires.FirstTail))
			} else {
				_, _ = w.Write([]byte(item.Wires.SecondWire))
			}
			return
		}
		if count == 1 {
			select {
			case <-ready:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte(item.Wires.Prefix))
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte(item.Wires.FirstTail))
			return
		}
		_, _ = w.Write([]byte(item.Wires.SecondWire))
	}))
	defer server.Close()
	ca := "-----BEGIN CERTIFICATE-----\n" + base64.StdEncoding.EncodeToString(server.Certificate().Raw) + "\n-----END CERTIFICATE-----\n"
	must(os.WriteFile(filepath.Join(home, "original-ca.pem"), []byte(ca), 0600))
	item.Input.Home = home
	item.Input.Origin = server.URL
	stats := item.Input.Stat
	item.Input.Stat = map[string]resumestate.Stat{}
	_ = stats
	// raw original stat values are only path-relocated, not Source projections.
	for path, value := range stats {
		item.Input.Stat[replace(path)] = value
	}
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	t.Setenv("ANTHROPIC_API_KEY", "offline-fixture-key")
	return runNative(t, item.Input, control)
}

func requireFailure(t *testing.T, result map[string]any, text string) {
	t.Helper()
	failure, ok := result["runError"].(map[string]any)
	if !ok || !strings.Contains(failure["message"].(string), text) {
		t.Fatalf("expected %q, got %#v", text, result["runError"])
	}
}

func TestActualBareSDKCustomHeadersUseExistingClientTransportWithoutChangingOriginalInput(t *testing.T) {
	var requests int
	var fetches, fulfilled int
	_, result := originalBoundary(t, nativeControls{Configure: func(options *sdkhost.Options) {
		options.OriginalEnv["ANTHROPIC_CUSTOM_HEADERS"] = "x-sdk-fixture: raw caller value\nx-client-request-id: original-custom-id"
		observe := options.ObserveRequest
		options.ObserveRequest = func(url string, headers http.Header, body json.RawMessage) {
			requests++
			if headers.Get("x-sdk-fixture") != "raw caller value" || headers.Get("x-client-request-id") != "original-custom-id" {
				t.Error("actual SDK request lost the caller's custom headers", headers)
			}
			observe(url, headers, body)
		}
		options.ObserveFetch = func(input tracecapture.FetchInput) {
			fetches++
			if input.Method != "POST" || !json.Valid(input.PlainBody) || input.Headers.Get("x-sdk-fixture") != "raw caller value" {
				t.Error("actual SDK fetch observer changed the network input", input.Method, input.Headers)
			}
			// The observer cannot alter the SDK's prepared request or actual
			// transport. The same physical server must still complete both ASKs.
			input.PlainBody[0] = 'x'
			input.Headers.Set("x-sdk-fixture", "changed observer copy")
		}
		options.ObserveNativeFetch = func(event provider.OwnerNativeEvent) {
			if event.Kind == "fetch-fulfilled" {
				fulfilled++
			}
		}
	}})
	if requests != 2 || fetches != 2 || fulfilled != 2 || len(result["requests"].([]string)) != 2 || result["runError"] != nil {
		t.Fatal("custom header environment changed the actual two-ASK lifecycle", requests, fetches, fulfilled, result["runError"])
	}
}
func TestOriginalHostRejectsBeforeAdmission(t *testing.T) {
	cases := []struct {
		name, contains string
		configure      func(*sdkhost.Options)
	}{
		{"missing-shared-services", "requires actual", func(o *sdkhost.Options) { o.Factory = nil }},
		{"nonbare", "non-bare", func(o *sdkhost.Options) { o.OriginalEnv["CLAUDE_CODE_SIMPLE"] = "false" }},
		{"unknown-env", "original environment", func(o *sdkhost.Options) { o.OriginalEnv["PRIVATE_UNSUPPORTED_CAPABILITY"] = "1" }},
		{"known-model", "accounting known", func(o *sdkhost.Options) { o.Model = "claude-opus-4-6" }},
		{"fast", "auth/fast", func(o *sdkhost.Options) { o.OriginalEnv["CLAUDE_CODE_DISABLE_FAST_MODE"] = "0" }},
		{"nonempty-original-file", "nonempty original settings", func(o *sdkhost.Options) {
			must(os.WriteFile(filepath.Join(o.CWD, ".claude", "settings.json"), []byte(`{"model":"original-model"}`), 0600))
			o.SettingsState.ResetSettingsCache()
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, result := originalBoundary(t, nativeControls{Configure: c.configure, AllowFailure: true})
			requireFailure(t, result, c.contains)
			if len(stdout) != 0 || len(result["requests"].([]string)) != 0 {
				t.Fatal("rejected host must not publish output or open model")
			}
		})
	}
}
func TestSerialAdmissionGuardsRealController(t *testing.T) {
	_, result := originalBoundary(t, nativeControls{Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		if _, e := sessioncontroller.NewSerialASK(nil, o.Owner); e == nil {
			t.Fatal("nil controller accepted")
		}
		a, e := sessioncontroller.NewSerialASK(o.Controller, o.Owner)
		if e != nil {
			return e
		}
		if e = h.RunOne(nil, context.Background(), inputs[0]); e == nil {
			t.Fatal("nil owner accepted")
		}
		return o.Owner.Run(context.Background(), func(s *cooperative.Scope) error {
			before := len(a.History())
			if e = h.RunOne(s, nil, inputs[0]); e == nil {
				t.Fatal("nil model domain accepted")
			}
			for _, invalid := range []protocol.JSONObject{protocol.NewJSONObject(), protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_request")}), func() protocol.JSONObject { v := inputs[0].Clone(); v.Set("priority", quote("now")); return v }()} {
				if _, e = a.Begin(s, invalid); e == nil {
					t.Fatal("unsupported admission accepted")
				}
			}
			if len(a.History()) != before {
				t.Fatal("rejected admission changed history")
			}
			if e = a.Accept(s, queryloop.Message{}); e == nil {
				t.Fatal("unstarted accept")
			}
			if e = a.Record(s, nil); e == nil {
				t.Fatal("unstarted record")
			}
			if e = a.Finally(s); e == nil {
				t.Fatal("unstarted finally")
			}
			if _, e = a.Begin(s, inputs[0]); e != nil {
				return e
			}
			if _, e = a.Begin(s, inputs[1]); e == nil {
				t.Fatal("overlapping ASK")
			}
			if e = a.Accept(s, queryloop.Message{}); e == nil {
				t.Fatal("undefined yielded object")
			}
			user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"original synthetic user"}]}`)}, protocol.JSONField{Name: "uuid", Value: quote("original-yielded-user")}, protocol.JSONField{Name: "timestamp", Value: quote("2023-11-14T22:13:20.000Z")})
			if e = a.Accept(s, queryloop.Message{Record: user}); e != nil {
				return e
			}
			if e = a.Record(s, a.History()); e != nil {
				return e
			}
			if _, e = a.Raw(requestcontext.Context{}); e != nil {
				return e
			}
			foreign := cooperative.New()
			defer foreign.Close()
			if e = foreign.Run(context.Background(), func(other *cooperative.Scope) error {
				if e = a.Finally(other); e == nil {
					t.Fatal("foreign owner published cache")
				}
				return nil
			}); e != nil {
				return e
			}
			if e = a.Finally(s); e != nil {
				return e
			}
			if _, e = a.Begin(s, inputs[0]); e == nil {
				t.Fatal("duplicate SDK command silently starts model")
			}
			return nil
		})
	}})
	if result["runError"] != nil {
		t.Fatal(result["runError"])
	}
	if len(result["requests"].([]string)) != 0 {
		t.Fatal("guard component opened model")
	}
}
func TestOutputFailureAndStopStillFinalizeRealTurn(t *testing.T) {
	cases := []struct {
		name    string
		failAt  string
		receipt bool
		stop    bool
	}{
		{"init-fifo-fails", "system", false, false}, {"stream-fifo-fails", "stream_event", false, false}, {"replay-fifo-fails", "user", false, false}, {"assistant-fifo-fails", "assistant", false, false},
		{"init-stop", "system", true, true}, {"stream-stop", "stream_event", true, true}, {"assistant-stop", "assistant", true, true}, {"result-stop", "result", true, true},
		{"nil-receipt", "stream_event", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hit := false
			stdout, result := originalBoundary(t, nativeControls{AllowFailure: true, Configure: func(o *sdkhost.Options) {
				enqueue := o.Output.Enqueue
				if !c.receipt {
					o.Output.Enqueue = func(b []byte) error {
						if object(b).String("type") == c.failAt {
							hit = true
							return errors.New("original FIFO failure")
						}
						return enqueue(b)
					}
				} else {
					o.Output.Receipt = func(b []byte) *cooperative.Yield {
						if object(b).String("type") == c.failAt {
							hit = true
							if !c.stop {
								return nil
							}
						}
						receipt := cooperative.NewYield()
						action := cooperative.Resume
						if hit && c.stop {
							action = cooperative.Stop
						}
						receipt.Resolve(action, nil)
						return receipt
					}
				}
			}, Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
				return o.Owner.Run(context.Background(), func(s *cooperative.Scope) error { return h.RunOne(s, context.Background(), inputs[0]) })
			}})
			_ = stdout
			if !hit {
				t.Fatal("external boundary never reached")
			}
			phases := result["phases"].([]string)
			if !strings.Contains(strings.Join(phases, "|"), "ask:finally") {
				t.Fatal("ASK failure skipped cache/finally")
			}
			if !c.stop {
				requireFailure(t, result, map[bool]string{true: "consumer requires", false: "FIFO failure"}[c.receipt])
			}
		})
	}
}

// Live provider objects are accepted by the actual adapter. They are not
// JSON-cloned into a parallel history projection; future reads retain usage.
func TestAdapterRetainsOriginalLiveEnvelope(t *testing.T) {
	_, result := originalBoundary(t, nativeControls{Run: func(o sdkhost.Options, _ *sdkhost.Host, inputs []protocol.JSONObject) error {
		a, e := sessioncontroller.NewSerialASK(o.Controller, o.Owner)
		if e != nil {
			return e
		}
		return o.Owner.Run(context.Background(), func(s *cooperative.Scope) error {
			if _, e = a.Begin(s, inputs[0]); e != nil {
				return e
			}
			v := &streamhistory.Envelope{Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("assistant")}), Message: protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quote("assistant")}, protocol.JSONField{Name: "content", Value: json.RawMessage(`[{"type":"text","text":"original live"}]`)})}
			if e = a.Accept(s, queryloop.Message{Assistant: v}); e != nil {
				return e
			}
			history := a.History()
			if history[len(history)-1].Assistant != v {
				t.Fatal("live identity lost")
			}
			v.Message.Set("model", quote("late-original-model"))
			if got := a.History()[len(history)-1].Assistant.Message.String("model"); got != "late-original-model" {
				t.Fatal("late update lost")
			}
			return a.Finally(s)
		})
	}})
	if result["runError"] != nil {
		t.Fatal(result["runError"])
	}
}

func TestModelCancelLeavesOwnerAliveForPartialCheckpointAndSecondASK(t *testing.T) {
	var cancel context.CancelCauseFunc
	stopped := false
	stdout, result := originalBoundary(t, nativeControls{Configure: func(o *sdkhost.Options) {
		enqueue := o.Output.Enqueue
		o.Output.Enqueue = func(b []byte) error {
			event := object(b)
			if !stopped && event.String("type") == "stream_event" && object(event.Get("event")).String("type") == "content_block_stop" {
				stopped = true
				cancel(context.Canceled)
			}
			return enqueue(b)
		}
	}, Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		first, stop := context.WithCancelCause(context.Background())
		cancel = stop
		defer stop(nil)
		if e := o.Owner.Run(context.Background(), func(s *cooperative.Scope) error { return h.RunOne(s, first, inputs[0]) }); e != nil {
			return e
		}
		// The second original input uses a new model signal after genuine finally;
		// it does not replace the first accepted mutable history with a projection.
		return o.Owner.Run(context.Background(), func(s *cooperative.Scope) error { return h.RunOne(s, context.Background(), inputs[1]) })
	}})
	if !stopped {
		t.Fatal("original cancellation boundary never reached")
	}
	if result["runError"] != nil {
		t.Fatal(result["runError"])
	}
	if !strings.Contains(string(stdout), "[Request interrupted by user]") {
		t.Fatal("actual outer Abort path did not emit original interruption")
	}
	if len(result["requests"].([]string)) != 2 {
		t.Fatal("owner/model lifetime mixed; second ASK did not request")
	}
	phases := strings.Join(result["phases"].([]string), "|")
	if strings.Count(phases, "checkpoint:settled") != 2 || strings.Count(phases, "ask:finally") != 2 {
		t.Fatal("aborted ASK did not complete real checkpoint and cache finally")
	}
}

func TestSecondHostCannotBorrowAlreadyOwnedWriter(t *testing.T) {
	_, result := originalBoundary(t, nativeControls{Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		if _, e := sdkhost.New(o); e == nil || !strings.Contains(e.Error(), "already has") {
			t.Fatalf("independent drain owner replaced: %v", e)
		}
		return nil
	}})
	if result["runError"] != nil {
		t.Fatal(result["runError"])
	}
}

func TestMalformedSDKCannotCreateModelOrCorruptAcceptedHistory(t *testing.T) {
	cases := []struct {
		name   string
		change func(protocol.JSONObject)
	}{
		{"unknown-original-field", func(v protocol.JSONObject) { v.Set("preparedHistory", json.RawMessage(`[]`)) }},
		{"nontext-media", func(v protocol.JSONObject) {
			v.Set("message", json.RawMessage(`{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"original"}}]}`))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, result := originalBoundary(t, nativeControls{AllowFailure: true, Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
				v := inputs[0].Clone()
				c.change(v)
				return o.Owner.Run(context.Background(), func(s *cooperative.Scope) error { return h.RunOne(s, context.Background(), v) })
			}})
			if result["runError"] == nil || len(result["requests"].([]string)) != 0 || len(result["historySource"].([]string)) != 2 {
				t.Fatal("invalid original SDK input escaped before-effects guard")
			}
		})
	}
}

func TestSerialRawRejectsAlternateHistoryAndInvalidProgrammaticBytes(t *testing.T) {
	_, result := originalBoundary(t, nativeControls{Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		a, e := sessioncontroller.NewSerialASK(o.Controller, o.Owner)
		if e != nil {
			return e
		}
		return o.Owner.Run(context.Background(), func(s *cooperative.Scope) error {
			if _, e = a.Begin(s, inputs[0]); e != nil {
				return e
			}
			if _, e = a.Raw(requestcontext.Context{History: []protocol.JSONObject{}}); e == nil {
				t.Fatal("alternate prepared history accepted")
			}
			invalid := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: json.RawMessage("invalid")}, protocol.JSONField{Name: "uuid", Value: quote("programmer-bad-record")})
			if e = a.Accept(s, queryloop.Message{Record: invalid}); e != nil {
				return e
			}
			if _, e = a.Raw(requestcontext.Context{}); e == nil {
				t.Fatal("invalid accepted programmatic bytes silently disappeared")
			}
			invalid.Set("message", json.RawMessage(`{"role":"user","content":"restored original valid fixture field"}`))
			return a.Finally(s)
		})
	}})
	if result["runError"] != nil {
		t.Fatal(result["runError"])
	}
}

func TestCheckpointNativeReadOnlyFailurePreservesFinally(t *testing.T) {
	backup := ""
	faultPath := ""
	var observed error
	_, result := originalBoundary(t, nativeControls{Configure: func(o *sdkhost.Options) {
		o.UnhandledRecord = func(_ *cooperative.Scope, reason error) { observed = reason }
		enqueue := o.Output.Enqueue
		changed := false
		o.Output.Enqueue = func(b []byte) error {
			if !changed && object(b).String("type") == "assistant" {
				changed = true
				project := projectpath.Sanitize(o.CWD)
				target := filepath.Join(o.CWD, ".claude", "projects", project, o.Controller.SessionID()+".jsonl")
				faultPath = target
				backup = filepath.Join(o.CWD, "native-original-backup.jsonl")
				original, e := os.ReadFile(target)
				must(e)
				must(os.WriteFile(backup, original, 0600))
				must(os.Chmod(target, 0400))
			}
			return enqueue(b)
		}
	}, Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		if err := o.Owner.Run(context.Background(), func(s *cooperative.Scope) error { return h.RunOne(s, context.Background(), inputs[0]) }); err != nil {
			return err
		}
		// Test-only observation of the late native rejection after ASK returns.
		// This does not add a production print forceExit join or an ASK await.
		return h.WaitTranscriptRuntime(context.Background())
	}})
	var native *os.PathError
	if backup == "" || !errors.As(observed, &native) || !errors.Is(observed, syscall.EACCES) || native.Op != "open" || native.Path != faultPath {
		t.Fatal("actual detached native append fault not observed", observed)
	}
	if result["runError"] != nil {
		t.Fatal("late unhandled Record rejection became synchronous ASK error", result["runError"])
	}
	original, e := os.ReadFile(backup)
	if e != nil || !strings.Contains(string(original), `"uuid":"old-user"`) {
		t.Fatal("read-only append fault destroyed original fixture backup")
	}
	phases := strings.Join(result["phases"].([]string), "|")
	if !strings.Contains(phases, "checkpoint:settled") || !strings.Contains(phases, "ask:finally") {
		t.Fatal("real failed write skipped checkpoint/cache finally")
	}
}

func TestClosedHostRejectsBeforeNewAdmission(t *testing.T) {
	_, result := originalBoundary(t, nativeControls{Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		if e := h.Close(context.Background()); e != nil {
			return e
		}
		before := len(h.History())
		e := o.Owner.Run(context.Background(), func(s *cooperative.Scope) error { return h.RunOne(s, context.Background(), inputs[0]) })
		if e != cooperative.ErrClosed || len(h.History()) != before {
			t.Fatalf("closed admission changed history: %v", e)
		}
		return nil
	}})
	if result["runError"] != nil {
		t.Fatal(result["runError"])
	}
}
func TestOwnerLifecycleCancelDoesNotBecomeModelAPIError(t *testing.T) {
	var stop context.CancelFunc
	cancelled := false
	_, result := originalBoundary(t, nativeControls{AllowFailure: true, Configure: func(o *sdkhost.Options) {
		enqueue := o.Output.Enqueue
		o.Output.Enqueue = func(b []byte) error {
			if !cancelled && object(b).String("type") == "stream_event" {
				cancelled = true
				stop()
			}
			return enqueue(b)
		}
	}, Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		life, cancel := context.WithCancel(context.Background())
		stop = cancel
		defer cancel()
		return o.Owner.Run(life, func(s *cooperative.Scope) error { return h.RunOne(s, context.Background(), inputs[0]) })
	}})
	if !cancelled || result["runError"] == nil {
		t.Fatal("actual owner wait cancellation not propagated")
	}
	for _, message := range result["historySource"].([]string) {
		if strings.Contains(message, "API Error:") {
			t.Fatal("owner failure presented as provider terminal message")
		}
	}
	if !strings.Contains(strings.Join(result["phases"].([]string), "|"), "ask:finally") {
		t.Fatal("owner wait failure skipped serial cache finally")
	}
}
