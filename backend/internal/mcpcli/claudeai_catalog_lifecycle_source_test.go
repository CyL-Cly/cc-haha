package mcpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestCatalogMemoAndClearFlightAgainstActualSourceTLS(t *testing.T) {
	for _, mode := range []string{"success", "failed-memo", "clear-flight"} {
		t.Run(mode, func(t *testing.T) { catalogMemoSource(t, mode) })
	}
}
func catalogMemoSource(t *testing.T, mode string) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	_ = os.MkdirAll(home, 0700)
	physical := filepath.Join(home, "owned-input.json")
	_ = os.WriteFile(physical, []byte(`{"physical":"owned"}`), 0600)
	makePeer := func() (*httptest.Server, *int, *sync.Mutex, chan struct{}) {
		count := new(int)
		mu := &sync.Mutex{}
		released := make(chan struct{})
		ready := make(chan struct{})
		peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/mcp_servers":
				mu.Lock()
				*count++
				ordinal := *count
				mu.Unlock()
				if ordinal == 1 {
					close(ready)
				}
				select {
				case <-released:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "failed-memo" {
					w.WriteHeader(503)
					_, _ = io.WriteString(w, `{"error":"owned failure"}`)
				} else {
					if ordinal == 1 {
						_, _ = io.WriteString(w, `{"data":[{"display_name":"First","id":"first","url":"https://owned.invalid/first"}]}`)
					} else {
						_, _ = io.WriteString(w, `{"data":[{"display_name":"Second","id":"second","url":"https://owned.invalid/second"}]}`)
					}
				}
			case "/control/physical-read":
				<-ready
				w.WriteHeader(204)
			case "/control/release":
				close(released)
				w.WriteHeader(204)
			}
		}))
		return peer, count, mu, released
	}
	source, sourceCount, sourceMu, _ := makePeer()
	defer source.Close()
	ca := filepath.Join(root, "source-ca.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: source.Certificate().Raw}), 0600)
	tokens := json.RawMessage(`{"accessToken":"fake-owned-catalog","scopes":["user:mcp_servers"]}`)
	input, _ := json.Marshal(map[string]any{"homeDir": home, "address": source.URL, "physicalFile": physical, "tokens": tokens, "clear": mode == "clear-flight"})
	fixture, _ := filepath.Abs("claudeai_catalog_lifecycle.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_CLAUDEAI_CATALOG_FIXTURE=1", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "NODE_EXTRA_CA_CERTS=" + ca}
	cmd.Stdin = bytes.NewReader(input)
	var sourceErr bytes.Buffer
	cmd.Stderr = &sourceErr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source memo", err, sourceErr.String())
	}
	var oracle map[string]any
	if err = json.Unmarshal(out, &oracle); err != nil {
		t.Fatal(err, string(out))
	}
	peer, count, mu, released := makePeer()
	defer peer.Close()
	catalog := ClaudeAICatalog{HTTP: peer.Client(), BaseURL: peer.URL, Environment: map[string]string{}, Tokens: func(context.Context) (json.RawMessage, error) {
		return []byte(`{"claudeAiOauth":` + string(tokens) + `}`), nil
	}}
	defer catalog.Close()
	first := catalog.Begin(ctx)
	same := catalog.Begin(ctx)
	if first.entry != same.entry {
		t.Fatal("multi Begin did not share memo identity")
	}
	// Match the Source fixture's owned /control/physical-read barrier: its first
	// request has entered TLS before clear permits the next flight.
	firstDeadline := time.NewTimer(5 * time.Second)
	defer firstDeadline.Stop()
	firstPoll := time.NewTicker(time.Millisecond)
	defer firstPoll.Stop()
	for {
		mu.Lock()
		entered := *count
		mu.Unlock()
		if entered == 1 {
			break
		}
		select {
		case <-firstDeadline.C:
			t.Fatal("first actual TLS flight absent")
		case <-firstPoll.C:
		}
	}

	physicalData, e := os.ReadFile(physical)
	if e != nil {
		t.Fatal(e)
	}
	var second *ClaudeAICatalogFuture
	if mode == "clear-flight" {
		catalog.Clear()
		second = catalog.Begin(ctx)
		if second.entry == first.entry {
			t.Fatal("Clear retargeted old future")
		}
	}
	// Both producers must enter actual TLS before the controlled response finish.
	expected := 1
	if second != nil {
		expected = 2
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		mu.Lock()
		seen := *count
		mu.Unlock()
		if seen == expected {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("actual TLS flights did not enter", seen, expected)
		case <-poll.C:
		}
	}
	close(released)
	old := first.Await(ctx)
	newValue := catalog.Configurations(ctx)
	actual := map[string]any{"same": first.entry == same.entry, "hit": catalog.Begin(ctx).entry == func() *claudeAICatalogEntry {
		if second != nil {
			return second.entry
		}
		return first.entry
	}(), "physical": string(physicalData), "firstValue": decodeCatalog(t, old), "secondValue": decodeCatalog(t, newValue)}
	if !reflect.DeepEqual(oracle, actual) {
		t.Fatalf("actual Source%v Go%v", oracle, actual)
	}
	sourceMu.Lock()
	originalCount := *sourceCount
	sourceMu.Unlock()
	mu.Lock()
	actualCount := *count
	mu.Unlock()
	if originalCount != actualCount {
		t.Fatal("actual original memo requests", originalCount, actualCount)
	}
	old.Set("caller-injected", []byte(`{"private":true}`))
	if first.Await(ctx).Get("caller-injected") != nil || catalog.Configurations(ctx).Get("caller-injected") != nil {
		t.Fatal("caller changed memo through result clone")
	}
}
func decodeCatalog(t *testing.T, value protocol.JSONObject) any {
	t.Helper()
	raw, _ := value.MarshalJSON()
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
func TestCatalogWaiterCancellationAndCloseHeldTLSJoin(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(closed) }))
	defer peer.Close()
	catalog := ClaudeAICatalog{HTTP: peer.Client(), BaseURL: peer.URL, Environment: map[string]string{}, Tokens: func(context.Context) (json.RawMessage, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"fake-owned","scopes":["user:mcp_servers"]}}`), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flight := catalog.Begin(ctx)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("TLS request absent")
	}
	waiter, leave := context.WithCancel(context.Background())
	leave()
	if len(flight.Await(waiter).OwnProperties()) != 0 {
		t.Fatal("canceled waiter returned value")
	}
	select {
	case <-closed:
		t.Fatal("single waiter canceled shared HTTP")
	default:
	}
	done := make(chan struct{})
	go func() { catalog.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close failed to join HTTP")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("physical socket survived Close")
	}
	if len(flight.Await(context.Background()).OwnProperties()) != 0 {
		t.Fatal("aborted flight not original empty result")
	}
	if len(catalog.Begin(context.Background()).Await(context.Background()).OwnProperties()) != 0 {
		t.Fatal("closed Begin returned nonempty")
	}
}
func TestCatalogCloseCancelsAndJoinsActualTokenRead(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "fake-tokens.json")
	_ = os.WriteFile(file, []byte(`{"fake":"owned"}`), 0600)
	started, done := make(chan struct{}), make(chan struct{})
	catalog := ClaudeAICatalog{HTTP: &http.Client{}, Environment: map[string]string{}, Tokens: func(ctx context.Context) (json.RawMessage, error) {
		if _, err := os.ReadFile(file); err != nil {
			return nil, err
		}
		close(started)
		<-ctx.Done()
		close(done)
		return nil, ctx.Err()
	}}
	_ = catalog.Begin(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("physical token read absent")
	}
	catalog.Close()
	select {
	case <-done:
	default:
		t.Fatal("Close returned before token read joined")
	}
	if len(catalog.Configurations(context.Background()).OwnProperties()) != 0 {
		t.Fatal("closed catalog refetched token")
	}

}

func TestCatalogCloseJoinsDetachedAndCurrentHeldTLSSockets(t *testing.T) {
	started, closed := make(chan struct{}, 2), make(chan struct{}, 2)
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		closed <- struct{}{}
	}))
	defer peer.Close()
	catalog := ClaudeAICatalog{HTTP: peer.Client(), BaseURL: peer.URL, Environment: map[string]string{}, Tokens: func(context.Context) (json.RawMessage, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"fake-two-owned-flights","scopes":["user:mcp_servers"]}}`), nil
	}}
	defer catalog.Close()
	waitStarted := func() {
		t.Helper()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("owned TLS flight absent")
		}
	}
	first := catalog.Begin(context.Background())
	waitStarted()
	catalog.Clear()
	second := catalog.Begin(context.Background())
	waitStarted()
	if first.entry == second.entry {
		t.Fatal("Clear reused the detached flight")
	}
	done := make(chan struct{})
	go func() { catalog.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close failed to join both actual TLS flights")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("detached/current socket survived Close", i)
		}
	}
	for _, flight := range []*ClaudeAICatalogFuture{first, second} {
		if len(flight.Await(context.Background()).OwnProperties()) != 0 {
			t.Fatal("closed flight not empty")
		}
	}
	catalog.mu.Lock()
	active := len(catalog.active)
	catalog.mu.Unlock()
	if active != 0 {
		t.Fatal("Close returned before worker bookkeeping joined", active)
	}
	if len(catalog.Begin(context.Background()).Await(context.Background()).OwnProperties()) != 0 {
		t.Fatal("closed catalog re-opened")
	}
}

func TestCatalogCanceledWaiterKeepsSuccessfulSibling(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, `{"data":[{"display_name":"Sibling","id":"sibling"}]}`)
		case <-r.Context().Done():
			t.Error("single waiter aborted shared producer")
		}
	}))
	defer peer.Close()
	catalog := ClaudeAICatalog{HTTP: peer.Client(), BaseURL: peer.URL, Environment: map[string]string{}, Tokens: func(context.Context) (json.RawMessage, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"fake-owned","scopes":["user:mcp_servers"]}}`), nil
	}}
	defer catalog.Close()
	flight := catalog.Begin(context.Background())
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("actual shared TLS absent")
	}
	waiter, cancel := context.WithCancel(context.Background())
	cancel()
	if len(flight.Await(waiter).OwnProperties()) != 0 {
		t.Fatal("canceled waiter nonempty")
	}
	sibling := make(chan protocol.JSONObject, 1)
	go func() { sibling <- catalog.Begin(context.Background()).Await(context.Background()) }()
	close(release)
	select {
	case value := <-sibling:
		if value.Get("claude.ai Sibling") == nil {
			t.Fatal("sibling lost shared result", value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sibling failed to settle")
	}
}

func TestCatalogOriginalFiveSecondTimeoutPhysicalSourceAndSocket(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	_ = os.MkdirAll(home, 0700)
	newPeer := func() (*httptest.Server, chan struct{}) {
		closed := make(chan struct{})
		peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done(); close(closed) }))
		return peer, closed
	}
	source, sourceClosed := newPeer()
	defer source.Close()
	ca := filepath.Join(root, "timeout-ca.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: source.Certificate().Raw}), 0600)
	tokens := json.RawMessage(`{"accessToken":"fake-five-seconds","scopes":["user:mcp_servers"]}`)
	input, _ := json.Marshal(map[string]any{"homeDir": home, "address": source.URL, "tokens": tokens, "timeout": true})
	fixture, _ := filepath.Abs("claudeai_catalog_lifecycle.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_CLAUDEAI_CATALOG_FIXTURE=1", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "NODE_EXTRA_CA_CERTS=" + ca}
	cmd.Stdin = bytes.NewReader(input)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	start := time.Now()
	out, err := cmd.Output()
	sourceElapsed := time.Since(start)
	if err != nil {
		t.Fatal(err, diagnostics.String())
	}
	var expected map[string]any
	_ = json.Unmarshal(out, &expected)
	if expected["same"] != true || expected["hit"] != true || expected["timeoutMs"] != float64(5000) || len(expected["value"].(map[string]any)) != 0 {
		t.Fatal("actual Source timeout result", string(out))
	}
	select {
	case <-sourceClosed:
	case <-time.After(time.Second):
		t.Fatal("original timeout socket survived")
	}
	peer, closed := newPeer()
	defer peer.Close()
	catalog := ClaudeAICatalog{HTTP: peer.Client(), BaseURL: peer.URL, Environment: map[string]string{}, Tokens: func(context.Context) (json.RawMessage, error) {
		return []byte(`{"claudeAiOauth":` + string(tokens) + `}`), nil
	}}
	defer catalog.Close()
	start = time.Now()
	flight := catalog.Begin(ctx)
	value := flight.Await(ctx)
	elapsed := time.Since(start)
	if len(value.OwnProperties()) != 0 || catalog.Begin(ctx).entry != flight.entry {
		t.Fatal("native timeout result/memo")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Go timeout socket survived")
	}
	if elapsed < 4500*time.Millisecond || sourceElapsed < 4500*time.Millisecond {
		t.Fatal("original 5s timeout shortened", elapsed, sourceElapsed)
	}
	t.Logf("actual unchanged Source five-second timeout %s; Go %s", sourceElapsed, elapsed)
}
