//go:build darwin

package modelyield

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

type streamPoolHeadersReceipt struct {
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
	marker string
}

func (receipt *streamPoolHeadersReceipt) Write(data []byte) (int, error) {
	n, err := receipt.buffer.Write(data)
	if strings.Contains(receipt.buffer.String(), receipt.marker) {
		receipt.once.Do(func() { close(receipt.ready) })
	}
	return n, err
}

func (receipt *streamPoolHeadersReceipt) String() string { return receipt.buffer.String() }

func TestActualSourceStreamTransportRetryOnlyDisablesPoolWithoutPartialMessage(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun not configured")
	}
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-message-start", true: "after-message-start"}[partial], func(t *testing.T) {
			var mu sync.Mutex
			var ports []string
			posts := 0
			cutReady := make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				mu.Lock()
				ports = append(ports, r.RemoteAddr)
				posts++
				n := posts
				ready := cutReady
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if n == 1 {
					w.Header().Set("X-Owned-Stream-Cutpoint", "headers")
					w.Header().Set("Content-Length", "1000000")
					w.WriteHeader(200)
					if partial {
						_, _ = io.WriteString(w, strings.Split(queryWire("normal"), "event: content_block_start")[0])
					}
					w.(http.Flusher).Flush()
					select {
					case <-ready:
					case <-r.Context().Done():
						return
					}
					c, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					_ = c.Close()
					return
				}
				_, _ = io.WriteString(w, queryWire("normal"))
			}))
			defer peer.Close()
			home := t.TempDir()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-stream-retry.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_STREAM_RETRY_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
			payload, _ := json.Marshal(map[string]any{"home": home, "origin": peer.URL, "retries": "1", "maxRetries": "0", "poolFollowup": 2, "poolHeadersReceipt": true, "poolCutAfterMessageStart": partial})
			command.Stdin = bytes.NewReader(payload)
			marker := "owned-stream-headers-ready\n"
			if partial {
				marker = "owned-stream-message-start-ready\n"
			}
			stderr := streamPoolHeadersReceipt{ready: cutReady, marker: marker}
			command.Stderr = &stderr
			data, e := command.Output()
			if e != nil {
				t.Fatal("whole Source queryModel/transport/next query", e, stderr.String())
			}
			var source struct {
				Posts             int
				KeepaliveDisabled bool
				Capabilities      struct{ Blocked []any }
			}
			if e = json.Unmarshal(data, &source); e != nil || source.Posts != 3 || len(source.Capabilities.Blocked) != 0 || source.KeepaliveDisabled == partial {
				t.Fatal("Source pool/partial result", e, string(data))
			}
			mu.Lock()
			expected := append([]string{}, ports...)
			ports = nil
			posts = 0
			cutReady = make(chan struct{})
			var cutOnce sync.Once
			mu.Unlock()
			owner := cooperative.New()
			defer owner.Close()
			session, _ := modelaccounting.NewOwnedSession(owner, nil)
			binding, _ := modelaccounting.NewMainContext(owner)
			var pool *providernative.Pool
			if e = owner.Run(ctx, func(scope *cooperative.Scope) error {
				var e error
				pool, e = providernative.NewPool(owner, scope)
				return e
			}); e != nil {
				t.Fatal(e)
			}
			defer pool.Close()
			input := queuedInput(peer.URL)
			input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
			input.Context.Host.Env["CLAUDE_CODE_MAX_RETRIES"] = "0"
			policy := map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "1", "CLAUDE_ENABLE_STREAM_WATCHDOG": "0"}
			e = owner.Run(ctx, func(scope *cooperative.Scope) error {
				for range 3 {
					_, e := Run(ctx, input, Dependencies{Session: session, AgentContext: binding, StreamPolicy: PreserveNativeStreamErrorPolicyOriginalEnvironment(policy), Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, NativeOpenIO: providernative.NewPooledFactory(owner, scope, func(event provider.OwnerNativeEvent) {
						if !partial && event.Kind == "fetch-fulfilled" {
							cutOnce.Do(func() { close(cutReady) })
						}
					}, pool), BodyIOAwait: func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
						// Same actual body bridge as sdkhost.requestDependencies.
						if kind != provider.OwnerBodyRead && kind != provider.OwnerErrorBody {
							return nil, cooperative.ErrRestricted
						}
						return scope.AwaitBytes(cooperative.StreamNext, work)
					}, UUID: func() string { return "private-go-id" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil }}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
						if partial && value.Update != nil && len(value.Update.Event) != 0 {
							var event struct{ Type string }
							if err := json.Unmarshal(value.Update.Event, &event); err != nil {
								return 0, err
							}
							if event.Type == "message_start" {
								cutOnce.Do(func() { close(cutReady) })
							}
						}
						return requestretry.Resume, nil
					}})
					if e != nil {
						return e
					}
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			actual := append([]string{}, ports...)
			mu.Unlock()
			if len(actual) != 3 || len(expected) != 3 || (actual[1] == actual[2]) != (expected[1] == expected[2]) {
				t.Fatal("actual following query pool policy drift", actual, expected, source.KeepaliveDisabled)
			}
		})
	}
}
