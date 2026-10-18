//go:build darwin

package requestretry

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func TestActualSourceStaleTLSRetryDisablesNativePoolingForFollowingClients(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun not configured")
	}
	var mu sync.Mutex
	var ports []string
	posts := 0
	native := false
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ports = append(ports, r.RemoteAddr)
		if r.Method == http.MethodPost {
			posts++
		}
		n, isNative := posts, native
		mu.Unlock()
		if r.Method == http.MethodHead {
			w.WriteHeader(204)
			return
		}
		_, _ = io.ReadAll(r.Body)
		if n == 1 {
			c, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			_ = c.Close()
			return
		}
		if isNative {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, responseWire)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"owned-pool","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		}
	}))
	defer peer.Close()
	home := t.TempDir()
	if e := os.MkdirAll(filepath.Join(home, ".claude"), 0700); e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(home, "pool-ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-http-pool.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HTTP_POOL_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "USER_TYPE=external", "NODE_EXTRA_CA_CERTS=" + ca, "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"home": home, "peer": peer.URL, "staleFirst": true})
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, e := command.Output()
	if e != nil {
		t.Fatal("whole Source withRetry TLS stale rejection", e, stderr.String())
	}
	var result struct {
		Responses, Retries int
		Blocked            []any
	}
	if e = json.Unmarshal(data, &result); e != nil || result.Responses != 2 || result.Retries != 1 || len(result.Blocked) != 0 {
		t.Fatal(e, string(data))
	}
	mu.Lock()
	sourcePorts := append([]string{}, ports...)
	ports = nil
	posts = 0
	native = true
	mu.Unlock()
	if len(sourcePorts) != 4 {
		t.Fatal("actual Source wire count", sourcePorts)
	}
	owner := cooperative.New()
	defer owner.Close()
	var pool *providernative.Pool
	if e = owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		pool, e = providernative.NewPool(owner, scope)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	// The actual init HEAD is independent; this retry consumer exercises SDK
	// creation and its original outer withRetry after the HEAD phase.
	state, e := tlsconfig.NewState(os.ReadFile)
	if e != nil {
		t.Fatal(e)
	}
	notices := 0
	e = owner.Run(ctx, func(scope *cooperative.Scope) error {
		input, deps := standardInput(), standardDeps()
		input.Context.Host.Env["ANTHROPIC_BASE_URL"] = peer.URL
		input.Context.Host.Env["NODE_EXTRA_CA_CERTS"] = ca
		input.Context.Host.Env["CLAUDE_CODE_MAX_RETRIES"] = "1"
		deps.TLSState = state
		deps.NativeOpenIO = providernative.NewPooledFactory(owner, scope, nil, pool)
		deps.Sleep = func(ctx context.Context, millis float64) error {
			_, e := scope.AwaitBytes(cooperative.RetryTimer, func(wait context.Context) ([]byte, error) {
				timer := time.NewTimer(time.Duration(millis * float64(time.Millisecond)))
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-wait.Done():
					return nil, wait.Err()
				case <-timer.C:
					return nil, nil
				}
			})
			return e
		}
		deps.Notice = func(context.Context, retrypolicy.RetryNotice) (NoticeAction, error) { notices++; return Resume, nil }
		for range 2 {
			stream, e := Open(ctx, input, deps)
			if e != nil {
				return e
			}
			for {
				_, e = stream.Next()
				if e == io.EOF {
					break
				}
				if e != nil {
					_ = stream.Close()
					return e
				}
			}
			if e = stream.Close(); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal("actual native stale withRetry/next client", e)
	}
	mu.Lock()
	actual := append([]string{}, ports...)
	mu.Unlock()
	if notices != 1 || len(actual) != 3 || actual[0] == actual[1] || actual[1] == actual[2] {
		t.Fatal("retry did not disable future native connection reuse", notices, actual, sourcePorts)
	}
}
