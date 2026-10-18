package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func runSourceTimeoutFixture(t *testing.T, input any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-timeouts.fixture.ts")
	ctx, stop := context.WithTimeout(context.Background(), 25*time.Second)
	defer stop()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MCP_TIMEOUTS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=private-loopback-fixture", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source timeout %v %.2500s", err, stderr.String())
	}
	var output protocol.JSONObject
	if err := output.UnmarshalJSON(data); err != nil {
		t.Fatal(err, string(data))
	}
	return output
}

func TestSourceTimeoutDecimalPrefixesDefaultsAndNegativeValues(t *testing.T) {
	values := []any{nil, "", "0", "-0", "NaN", "not-a-number", "12.8suffix", "+007ms", "-7x", "\ufeff\u00a0 42x", "\u008520", "0x20", "Infinity", "100000000000000000001", "1000000000000000000000000000000", strings.Repeat("9", 400)}
	want := runSourceTimeoutFixture(t, map[string]any{"mode": "getters", "values": values})
	var rows []protocol.JSONObject
	_ = json.Unmarshal(want.Get("rows"), &rows)
	for index, value := range values {
		text, _ := value.(string)
		connection, tool := sourceTimeoutMilliseconds(text, 30000), sourceTimeoutMilliseconds(text, 100_000_000)
		got := object(field("connection", sourceMillisecondsJSON(connection)), field("tool", sourceMillisecondsJSON(tool)), field("connectionText", sourceMillisecondsString(connection)), field("toolSecondsText", sourceMillisecondsString(math.Floor(tool/1000))))
		if !bytes.Equal(raw(got), raw(rows[index])) {
			t.Errorf("Source parseInt/default %q differs: Go %s Source %s", text, raw(got), raw(rows[index]))
		}
	}
}

const timeoutGoRequestHeader = "X-Cc-Haha-Timeout-Go"
const timeoutGoRequestMarker = "owned-go-initialize"

type timeoutPhysicalPeer struct {
	server          *httptest.Server
	mu              sync.Mutex
	held            map[string]chan struct{}
	wire            []json.RawMessage
	initializeDelay time.Duration
	closed          chan struct{}
	noticeChanged   chan struct{}
	goStarted       chan struct{}
	goClosed        chan struct{}
	goInitialize    int
}

func newTimeoutPhysicalPeer(t *testing.T, delay time.Duration) *timeoutPhysicalPeer {
	t.Helper()
	p := &timeoutPhysicalPeer{held: map[string]chan struct{}{}, initializeDelay: delay, closed: make(chan struct{}, 20), noticeChanged: make(chan struct{}, 1), goStarted: make(chan struct{}, 20), goClosed: make(chan struct{}, 20)}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/control/cancel-count" {
			count, _ := strconv.Atoi(r.URL.Query().Get("count"))
			for len(p.notices()) < count {
				select {
				case <-p.noticeChanged:
				case <-r.Context().Done():
					return
				}
			}
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(data)
		p.mu.Lock()
		p.wire = append(p.wire, append(json.RawMessage(nil), data...))
		p.mu.Unlock()
		if message.Get("id") == nil {
			if message.String("method") == "notifications/cancelled" {
				select {
				case p.noticeChanged <- struct{}{}:
				default:
				}
				params, _ := message.Object("params")
				p.mu.Lock()
				channel := p.held[string(params.Get("requestId"))]
				p.mu.Unlock()
				if channel != nil {
					select {
					case channel <- struct{}{}:
					default:
					}
				}
			}
			w.WriteHeader(202)
			return
		}
		var result json.RawMessage
		switch message.String("method") {
		case "initialize":
			goRequest := r.Header.Get(timeoutGoRequestHeader) == timeoutGoRequestMarker
			if goRequest {
				p.mu.Lock()
				p.goInitialize++
				p.mu.Unlock()
				p.goStarted <- struct{}{}
			}
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-r.Context().Done():
					p.closed <- struct{}{}
					if goRequest {
						p.goClosed <- struct{}{}
					}
					return
				}
			}
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-timeout-peer","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"pause","inputSchema":{"type":"object","properties":{"mode":{"type":"string"}}}}]}`)
		case "tools/call":
			params, _ := message.Object("params")
			arguments, _ := params.Object("arguments")
			if arguments.String("mode") == "hold" {
				channel := make(chan struct{}, 1)
				p.mu.Lock()
				p.held[string(message.Get("id"))] = channel
				p.mu.Unlock()
				select {
				case <-channel:
				case <-r.Context().Done():
				}
				p.closed <- struct{}{}
				result = []byte(`{"content":[{"type":"text","text":"late result must not win"}]}`)
			} else {
				select {
				case <-time.After(20 * time.Millisecond):
				case <-r.Context().Done():
					return
				}
				result = []byte(`{"content":[{"type":"text","text":"actual successful tool"}]}`)
			}
		default:
			t.Error("unexpected physical timeout MCP request", message)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(object(field("jsonrpc", "2.0"), protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *timeoutPhysicalPeer) notices() []protocol.JSONObject {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []protocol.JSONObject{}
	for _, data := range p.wire {
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(data)
		if message.String("method") == "notifications/cancelled" {
			params, _ := message.Object("params")
			out = append(out, params)
		}
	}
	return out
}

// This observer delegates every request to an owned real HTTP transport. It
// records admission and completion; it never cancels, releases or fakes a reply.
type timeoutGoAttempt struct {
	ctx        context.Context
	entryError error
	method     string
	marker     string
	done       chan struct{}
	err        error
}

type timeoutGoTransport struct {
	mu       sync.Mutex
	base     *http.Transport
	attempts []*timeoutGoAttempt
	closed   bool
	lateLive bool
	started  chan struct{}
}

func newTimeoutGoTransport(t *testing.T) *timeoutGoTransport {
	t.Helper()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	audit := &timeoutGoTransport{base: base, started: make(chan struct{}, 8)}
	t.Cleanup(base.CloseIdleConnections)
	// Recheck at cleanup as well: a send task may reach RoundTrip after the
	// Connect call returned. It must carry the already-cancelled lifetime.
	t.Cleanup(func() { audit.assertCancelled(t) })
	return audit
}

func (a *timeoutGoTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	attempt := &timeoutGoAttempt{ctx: request.Context(), entryError: request.Context().Err(), marker: request.Header.Get(timeoutGoRequestHeader), done: make(chan struct{})}
	// Inspect the replayable body without consuming the real transport input.
	if request.GetBody != nil {
		body, err := request.GetBody()
		if err == nil {
			data, readErr := io.ReadAll(body)
			_ = body.Close()
			var message protocol.JSONObject
			if readErr == nil && message.UnmarshalJSON(data) == nil {
				attempt.method = message.String("method")
			}
		}
	}
	a.mu.Lock()
	if a.closed && request.Context().Err() == nil {
		a.lateLive = true
	}
	a.attempts = append(a.attempts, attempt)
	a.mu.Unlock()
	select {
	case a.started <- struct{}{}:
	default:
	}
	response, err := a.base.RoundTrip(request)
	attempt.err = err
	close(attempt.done)
	return response, err
}

func (a *timeoutGoTransport) noteClosed(bool) {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
}

func (a *timeoutGoTransport) snapshot() ([]*timeoutGoAttempt, bool, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*timeoutGoAttempt(nil), a.attempts...), a.closed, a.lateLive
}

func (a *timeoutGoTransport) assertCancelled(t *testing.T) []*timeoutGoAttempt {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	joined := 0
	for {
		attempts, closed, lateLive := a.snapshot()
		if !closed || lateLive {
			t.Fatal("Go connection lifetime was not cancelled before late HTTP admission", closed, lateLive)
		}
		if len(attempts) > 1 {
			t.Fatal("timed-out Go initialize admitted extra HTTP requests", len(attempts))
		}
		for _, attempt := range attempts[joined:] {
			select {
			case <-attempt.done:
			case <-deadline.C:
				t.Fatal("admitted Go initialize RoundTrip did not physically finish after cancellation")
			}
			if attempt.method != "initialize" || attempt.marker != timeoutGoRequestMarker || !errors.Is(attempt.ctx.Err(), context.Canceled) || attempt.err == nil {
				t.Fatal("Go initialize cancellation/completion receipt differs", attempt.method, attempt.marker, attempt.ctx.Err(), attempt.err)
			}
		}
		joined = len(attempts)
		next, _, _ := a.snapshot()
		if len(next) == joined {
			return attempts
		}
	}
}

// This is an additional deterministic counterexample, not the original no-Auth
// negative row's traffic. Token is the real headers() producer before native
// HTTP admission; only its completion is held, without replacing HTTP or time.
type timeoutBeforeHTTPAuth struct {
	entered    chan struct{}
	cancelled  chan struct{}
	release    chan struct{}
	once       sync.Once
	calls      atomic.Int32
	entryError error
}

func (a *timeoutBeforeHTTPAuth) Token(ctx context.Context) (string, error) {
	if a.calls.Add(1) != 1 {
		return "", errors.New("unexpected repeated isolated auth producer")
	}
	a.entryError = ctx.Err()
	close(a.entered)
	<-ctx.Done()
	close(a.cancelled)
	<-a.release
	return "", nil
}

func (a *timeoutBeforeHTTPAuth) Authorize(context.Context, AuthChallenge) error {
	return errors.New("timeout fixture must not authorize an account")
}

func (a *timeoutBeforeHTTPAuth) allow() { a.once.Do(func() { close(a.release) }) }

func assertTimeoutBeforeNativeHTTPAdmission(t *testing.T, sourceError string) {
	t.Helper()
	peer := newTimeoutPhysicalPeer(t, 2*time.Second)
	audit := newTimeoutGoTransport(t)
	auth := &timeoutBeforeHTTPAuth{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(auth.allow)
	// This extra test-only bound prevents the fresh-diagnostic rendezvous from
	// hanging. It does not alter MCP_TIMEOUT, its 1ms timer, or Source bytes.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var reads atomic.Int32
	getter := func(ctx context.Context, name string) (string, error) {
		if name != "MCP_TIMEOUT" {
			return "", errors.New("unexpected timeout environment read")
		}
		if reads.Add(1) == 2 {
			// This call occurs in the fired deadline's fresh diagnostic branch.
			// Keep Close pending until the real send task reaches headers().
			select {
			case <-auth.entered:
			case <-ctx.Done():
				return "", context.Cause(ctx)
			}
		}
		return "-7", nil
	}
	before := time.Now()
	_, err := Connect(ctx, Config{Type: "http", URL: peer.server.URL, Headers: map[string]string{timeoutGoRequestHeader: timeoutGoRequestMarker}}, Options{Name: "timeout-peer", Environment: map[string]string{}, EnvironmentValue: getter, CWD: t.TempDir(), HTTPClient: &http.Client{Transport: audit}, Auth: auth, OnClose: audit.noteClosed})
	if err == nil || err.Error() != sourceError || time.Since(before) > time.Second {
		t.Fatal("early deadline changed Source diagnostic or original elapsed bound", err, sourceError, time.Since(before))
	}
	select {
	case <-auth.cancelled:
	case <-ctx.Done():
		t.Fatal("deadline did not cancel the real pre-HTTP task context", context.Cause(ctx))
	}
	if auth.entryError != nil || auth.calls.Load() != 1 || reads.Load() != 2 {
		t.Fatal("pre-HTTP rendezvous did not traverse the actual pending send and fired diagnostic", auth.entryError, auth.calls.Load(), reads.Load())
	}
	if attempts, _, _ := audit.snapshot(); len(attempts) != 0 {
		t.Fatal("HTTP admission overtook the held real auth producer", len(attempts))
	}
	auth.allow()
	select {
	case <-audit.started:
	case <-ctx.Done():
		t.Fatal("released late send never reached its real HTTP transport", context.Cause(ctx))
	}
	attempts := audit.assertCancelled(t)
	if len(attempts) != 1 || !errors.Is(attempts[0].entryError, context.Canceled) {
		t.Fatal("late HTTP task did not enter with the already-cancelled lifetime", len(attempts))
	}
	peer.mu.Lock()
	admitted := peer.goInitialize
	peer.mu.Unlock()
	if admitted != 0 || len(peer.notices()) != 0 {
		t.Fatal("late cancelled task left an effective initialize or cancellation notification", admitted, peer.notices())
	}
	t.Log("additional delayed-Auth counterexample: fired -7 deadline, actual task context cancelled before native HTTP; released late RoundTrip joined, zero effective Go initialize")
}

func TestSourceConnectionTimeoutCapturedDelayFreshDiagnosticAndPhysicalCancel(t *testing.T) {
	for _, value := range []string{"80prefix", "-7"} {
		t.Run(value, func(t *testing.T) {
			peer := newTimeoutPhysicalPeer(t, 2*time.Second)
			input := map[string]any{"url": peer.server.URL, "connection": value, "calls": []any{}}
			if value == "80prefix" {
				input["connectionReload"] = map[string]any{"after": 20, "value": "800later"}
			}
			source := runSourceTimeoutFixture(t, input)
			if source.String("connection") != "failed" {
				t.Fatal("Source connection fixture did not time out", source)
			}
			var mu sync.Mutex
			current := value
			getter := func(context.Context, string) (string, error) { mu.Lock(); defer mu.Unlock(); return current, nil }
			var reload *time.Timer
			if value == "80prefix" {
				reload = time.AfterFunc(20*time.Millisecond, func() { mu.Lock(); current = "800later"; mu.Unlock() })
				defer reload.Stop()
			}
			audit := newTimeoutGoTransport(t)
			before := time.Now()
			_, err := Connect(context.Background(), Config{Type: "http", URL: peer.server.URL, Headers: map[string]string{timeoutGoRequestHeader: timeoutGoRequestMarker}}, Options{Name: "timeout-peer", Environment: map[string]string{}, EnvironmentValue: getter, CWD: t.TempDir(), HTTPClient: &http.Client{Transport: audit}, OnClose: audit.noteClosed})
			elapsed := time.Since(before)
			if err == nil || err.Error() != source.String("error") {
				t.Fatalf("Source timeout diagnostic differs Go %v Source %s", err, source.String("error"))
			}
			if elapsed > time.Second || value == "80prefix" && elapsed < 60*time.Millisecond {
				t.Fatal("reload moved physical timer or timer did not fire", elapsed)
			}
			// Join every actually admitted Go request, independently of Source.
			// A negative timer may win before any Go RoundTrip starts.
			attempts := audit.assertCancelled(t)
			if value == "80prefix" {
				if len(attempts) != 1 || attempts[0].entryError != nil {
					t.Fatal("positive timeout did not exercise live Go initialize HTTP", len(attempts))
				}
				select {
				case <-peer.goStarted:
				case <-time.After(time.Second):
					t.Fatal("positive timeout never reached actual Go initialize handler")
				}
				select {
				case <-peer.goClosed:
				case <-time.After(time.Second):
					t.Fatal("timed-out Go initialize physical socket survived")
				}
			}
			if len(peer.notices()) != 0 {
				t.Fatal("outer connection timeout invented initialize request cancellation", peer.notices())
			}
			if value == "-7" {
				t.Run("additional-pre-HTTP-late-task", func(t *testing.T) {
					assertTimeoutBeforeNativeHTTPAdmission(t, source.String("error"))
				})
			}
		})
	}
}

func TestSourceToolTimeoutReloadDefaultsExternalErrorAndPhysicalSDKCancellation(t *testing.T) {
	peer := newTimeoutPhysicalPeer(t, 0)
	calls := []map[string]any{{"timeout": "40suffix", "mode": "hold", "reload": map[string]any{"after": 10, "value": "900"}}, {"timeout": "200", "mode": "fast"}, {"timeout": "bad", "mode": "fast"}, {"timeout": "0", "mode": "fast"}, {"timeout": "-7", "mode": "hold"}, {"timeout": "1000000000000000000000000000000", "mode": "hold"}, {"timeout": strings.Repeat("9", 400), "mode": "hold"}, {"timeout": "200", "mode": "hold", "cancelAfter": 10}}
	source := runSourceTimeoutFixture(t, map[string]any{"url": peer.server.URL, "connection": "0", "calls": calls, "expectedCancellations": 5})
	var rows []protocol.JSONObject
	_ = json.Unmarshal(source.Get("rows"), &rows)
	if len(rows) != len(calls) {
		t.Fatal("Source tool receipts missing", source)
	}
	var mu sync.Mutex
	env := map[string]string{"MCP_TIMEOUT": "0"}
	getter := func(_ context.Context, key string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		return env[key], nil
	}
	client, err := Connect(context.Background(), Config{Type: "http", URL: peer.server.URL}, Options{Name: "timeout-peer", Environment: map[string]string{}, EnvironmentValue: getter, CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for index, call := range calls {
		mu.Lock()
		env["MCP_TOOL_TIMEOUT"] = call["timeout"].(string)
		mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		var abort, reload *time.Timer
		if call["cancelAfter"] != nil {
			abort = time.AfterFunc(10*time.Millisecond, cancel)
		}
		if call["reload"] != nil {
			reload = time.AfterFunc(10*time.Millisecond, func() { mu.Lock(); env["MCP_TOOL_TIMEOUT"] = "900"; mu.Unlock() })
		}
		settled := make(chan struct{})
		before := time.Now()
		result, err := client.CallToolSource(ctx, raw("pause"), object(field("mode", call["mode"])), protocol.NewJSONObject(), func() { close(settled) })
		elapsed := time.Since(before)
		if abort != nil {
			abort.Stop()
		}
		if reload != nil {
			reload.Stop()
		}
		select {
		case <-settled:
		case <-time.After(time.Second):
			t.Fatal("external tool timeout leaked raw SDK request", index)
		}
		cancel()
		wantError, _ := rows[index].Object("error")
		if wantError.IsObject() {
			if wantError.String("name") != "TelemetrySafeError" || string(wantError.Get("code")) != "null" {
				t.Fatalf("Source wrapper unexpectedly emitted raw SDK timeout/abort type: %s", raw(wantError))
			}
			var timeout *ToolTimeoutError
			var protocolFailure *ToolProtocolError
			if (!errors.As(err, &timeout) && !errors.As(err, &protocolFailure)) || err.Error() != wantError.String("message") {
				t.Fatalf("Source external timeout type/message differs row%d Go%T %v Source%s", index, err, err, raw(wantError))
			}
			if elapsed > time.Second {
				t.Fatal("actual tool timeout ignored environment", elapsed)
			}
		} else if err != nil {
			t.Fatal("Source successful/aborted tool rejected", index, err)
		} else if index == len(calls)-1 {
			if result.Get("content") != nil {
				t.Fatal("Source swallowed AbortError content differs", result)
			}
		} else {
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(result.Get("content"), &blocks)
			if len(blocks) != 1 || blocks[0].String("text") != "actual successful tool" {
				t.Fatal("actual tool success lost", result)
			}
		}
	}
	deadline := time.Now().Add(time.Second)
	notices := peer.notices()
	for len(notices) < 10 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		notices = peer.notices()
	}
	if len(notices) != 10 {
		t.Fatal("Source/native per-request timeout and abort notifications missing", len(notices), notices)
	}
	reasons := []string{}
	for _, notice := range notices {
		reasons = append(reasons, notice.String("reason"))
	}
	if !reflect.DeepEqual(reasons[:5], reasons[5:]) {
		t.Fatal("Source SDK cancellation reason/type differs", reasons)
	}
	for _, reason := range reasons {
		if reason == "" {
			t.Fatal("empty SDK cancellation reason")
		}
	}
}

func TestActualSourceDiscardDisabledPendingDiscoveryCompletion(t *testing.T) {
	listStarted, listCancelled := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/control/list-started" {
			select {
			case <-listStarted:
				w.WriteHeader(204)
			case <-r.Context().Done():
			}
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(data)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		if message.String("method") == "tools/list" {
			close(listStarted)
			<-r.Context().Done()
			close(listCancelled)
			return
		}
		if message.String("method") != "initialize" {
			t.Error("unexpected actual Source startup request", message)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.Get("id")), "result": map[string]any{"protocolVersion": LatestProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "actual-retired-source", "version": "1"}}})
	}))
	defer peer.Close()
	result := runSourceTimeoutFixture(t, map[string]any{"mode": "disabled-completion", "url": peer.URL, "calls": []any{}})
	if result.String("status") != "disabled" || string(result.Get("discarded")) != "true" || string(result.Get("tools")) != "0" {
		t.Fatalf("frozen Source completion resurrects disabled pending peer: %s", raw(result))
	}
	select {
	case <-listCancelled:
	case <-time.After(time.Second):
		t.Fatal("actual Source cache clear did not cancel physical pending tools/list")
	}
}
