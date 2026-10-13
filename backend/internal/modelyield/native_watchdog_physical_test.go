package modelyield

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestNativeWatchdogUsesActualBodyAndMainTimeoutConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	start := strings.Split(queryWire("normal"), "event: content_block_start")[0]
	tool := start + "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"held-tool\",\"name\":\"Read\",\"input\":{}}}\n\n"
	toolDone := tool + "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	for _, row := range []struct {
		name, prefix, retries string
		env                   map[string]string
	}{
		{"no first event", "", "0", map[string]string{}},
		{"bookkeeping preserves first token budget", start, "0", map[string]string{}},
		{"held local tool can retry idle", toolDone, "1", map[string]string{}},
		{"server tool forbids replay", strings.Replace(tool, `"type":"tool_use"`, `"type":"server_tool_use"`, 1), "1", map[string]string{}},
		{"tool input inactivity", tool, "1", map[string]string{"CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS": "500", "CLAUDE_STREAM_IDLE_TIMEOUT_MS": "500", "CLAUDE_STREAM_TOOL_INPUT_MAX_DURATION_MS": "50"}},
		{"overall duration", tool, "1", map[string]string{"CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS": "500", "CLAUDE_STREAM_IDLE_TIMEOUT_MS": "500", "CLAUDE_STREAM_TOOL_INPUT_MAX_DURATION_MS": "500", "CLAUDE_STREAM_MAX_DURATION_MS": "50"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			env := map[string]string{"CLAUDE_ENABLE_STREAM_WATCHDOG": "1", "CLAUDE_STREAM_IDLE_TIMEOUT_MS": "50", "CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS": "70", "CLAUDE_STREAM_MAX_DURATION_MS": "0", "CLAUDE_STREAM_TOOL_INPUT_MAX_DURATION_MS": "0", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": row.retries}
			for k, v := range row.env {
				env[k] = v
			}
			var posts, closed atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := posts.Add(1)
				defer closed.Add(1)
				w.Header().Set("content-type", "text/event-stream")
				w.Header().Set("request-id", fmt.Sprintf("watchdog-%d", n))
				w.Header().Set("date", "Tue, 14 Nov 2023 22:13:20 GMT")
				if n > 1 {
					fmt.Fprint(w, queryWire("normal"))
					return
				}
				fmt.Fprint(w, row.prefix)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer peer.Close()
			home := t.TempDir()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-stream-retry.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_STREAM_RETRY_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
			payload, _ := json.Marshal(map[string]any{"home": home, "origin": peer.URL, "retries": row.retries, "maxRetries": "2", "watchdog": env})
			cmd.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			raw, err := cmd.Output()
			if err != nil {
				t.Fatalf("actual MAIN %v %s", err, stderr.String())
			}
			var source struct {
				Posts        int32
				Yielded      []map[string]any
				Sleeps       []float64
				Capabilities struct{ Blocked []string }
			}
			if err = json.Unmarshal(raw, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
				t.Fatalf("Source %v %s", err, raw)
			}
			deadline := time.Now().Add(time.Second)
			for closed.Load() != posts.Load() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if closed.Load() != posts.Load() {
				t.Fatal("Source did not release stalled body")
			}
			posts.Store(0)
			closed.Store(0)
			owner := cooperative.New()
			defer owner.Close()
			session, _ := modelaccounting.NewOwnedSession(owner, nil)
			binding, _ := modelaccounting.NewMainContext(owner)
			input := queuedInput(peer.URL)
			input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
			input.Context.Host.Env["CLAUDE_CODE_MAX_RETRIES"] = "2"
			yielded := []map[string]any{}
			sleeps := []float64{}
			err = owner.Run(ctx, func(scope *cooperative.Scope) error {
				_, err := Run(ctx, input, Dependencies{Session: session, AgentContext: binding, StreamPolicy: PreserveNativeStreamErrorPolicyOriginalEnvironment(env), Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "private-go-id" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(_ context.Context, ms float64) error { sleeps = append(sleeps, ms); return nil }}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
					var raw []byte
					var err error
					if value.Control != nil {
						raw, err = value.Control.MarshalJSON()
					} else if value.Notice != nil {
						message, e := value.Notice.Envelope()
						if e != nil {
							return 0, e
						}
						raw, err = message.MarshalJSON()
					} else if value.Update != nil && value.Update.Envelope != nil {
						raw, err = value.Update.Envelope.MarshalJSON()
					} else if value.Update != nil {
						raw, err = value.Update.EventJSON()
					} else {
						return 0, fmt.Errorf("empty yield")
					}
					if err != nil {
						return 0, err
					}
					var record map[string]any
					if err = json.Unmarshal(raw, &record); err != nil {
						return 0, err
					}
					yielded = append(yielded, record)
					return requestretry.Resume, nil
				}})
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(time.Second)
			for closed.Load() != posts.Load() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if closed.Load() != posts.Load() {
				t.Fatal("Go did not release stalled body")
			}
			if posts.Load() != source.Posts || !reflect.DeepEqual(yielded, source.Yielded) || !reflect.DeepEqual(sleeps, source.Sleeps) {
				got, _ := json.Marshal(yielded)
				want, _ := json.Marshal(source.Yielded)
				t.Fatalf("watchdog consumer differs posts %d/%d\ngot %s\nwant %s", posts.Load(), source.Posts, got, want)
			}
		})
	}
}
