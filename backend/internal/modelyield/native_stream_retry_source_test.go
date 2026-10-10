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

func TestActualNativeStreamRetryAgainstFullOriginalQueryModel(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	apiError := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"private transient\"}}\n\n"
	completedText := strings.Split(queryWire("normal"), "event: message_delta")[0]
	completedTool := strings.Split(queryWire("normal"), "event: content_block_start")[0] + "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"private-completed-tool\",\"name\":\"Read\",\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	for _, item := range []struct {
		name, prefix, retries, errorWire string
		marker, always, stop             bool
	}{
		{name: "empty then success", retries: "2"},
		{name: "budget zero", retries: "0", always: true},
		{name: "budget two exhausted", retries: "2", always: true},
		{name: "integer prefix", retries: "1tail", always: true},
		{name: "invalid uses default", retries: "invalid", always: true},
		{name: "stop at recovery", retries: "2", stop: true},
		{name: "ordinary buffered text no API retry", prefix: completedText, retries: "2"},
		{name: "Codex buffered text retries", prefix: completedText, retries: "2", marker: true},
		{name: "Codex buffered text exhausted", prefix: completedText, retries: "1", marker: true, always: true},
		{name: "completed tool no replay", prefix: completedTool, retries: "2", marker: true},
		{name: "policy no replay", retries: "2", marker: true, errorWire: "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"code\":\"content_policy\",\"message\":\"private policy\"}}\n\n"},
	} {
		t.Run(item.name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				number := posts.Add(1)
				w.Header().Set("content-type", "text/event-stream")
				w.Header().Set("request-id", fmt.Sprintf("private-request-%d", number))
				if item.marker {
					w.Header().Set("x-cc-haha-openai-codex-stream", "1")
				}
				if item.always || number == 1 {
					errorWire := item.errorWire
					if errorWire == "" {
						errorWire = apiError
					}
					_, _ = fmt.Fprint(w, item.prefix+errorWire)
				} else {
					_, _ = fmt.Fprint(w, queryWire("normal"))
				}
			}))
			defer server.Close()
			home := t.TempDir()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-stream-retry.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_STREAM_RETRY_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
			payload, _ := json.Marshal(map[string]any{"home": home, "origin": server.URL, "retries": item.retries, "stopRecovery": item.stop})
			cmd.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			raw, err := cmd.Output()
			if err != nil {
				t.Fatalf("actual original queryModel %v %s", err, stderr.String())
			}
			var original struct {
				Posts        int32
				Yielded      []map[string]any
				Capabilities struct{ Blocked []string }
			}
			if err := json.Unmarshal(raw, &original); err != nil || len(original.Capabilities.Blocked) != 0 || posts.Load() != original.Posts {
				t.Fatalf("actual original result %v %s", err, raw)
			}
			posts.Store(0)
			owner := cooperative.New()
			defer owner.Close()
			session, _ := modelaccounting.NewOwnedSession(owner, nil)
			binding, _ := modelaccounting.NewMainContext(owner)
			input := queuedInput(server.URL)
			input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
			policyEnv := map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": item.retries, "CLAUDE_ENABLE_STREAM_WATCHDOG": "0"}
			yielded := []map[string]any{}
			err = owner.Run(ctx, func(scope *cooperative.Scope) error {
				_, err := Run(ctx, input, Dependencies{Session: session, AgentContext: binding, StreamPolicy: PreserveNativeStreamErrorPolicyOriginalEnvironment(policyEnv), Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "private-go-id" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return fmt.Errorf("unexpected creation sleep") }}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
					var raw []byte
					var err error
					if value.Control != nil {
						raw, err = value.Control.MarshalJSON()
					} else if value.Update != nil && value.Update.Envelope != nil {
						raw, err = value.Update.Envelope.MarshalJSON()
					} else if value.Update != nil {
						raw, err = value.Update.EventJSON()
					} else {
						return 0, fmt.Errorf("unexpected creation notice")
					}
					if err != nil {
						return 0, err
					}
					var record map[string]any
					if err = json.Unmarshal(raw, &record); err != nil {
						return 0, err
					}
					yielded = append(yielded, record)
					if item.stop && value.Control != nil {
						return requestretry.Stop, nil
					}
					return requestretry.Resume, nil
				}})
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if posts.Load() != original.Posts || !reflect.DeepEqual(yielded, original.Yielded) {
				got, _ := json.Marshal(yielded)
				want, _ := json.Marshal(original.Yielded)
				t.Fatalf("actual stream resend/yield drift POSTs %d want%d\ngot %s\nwant%s", posts.Load(), original.Posts, got, want)
			}
		})
	}
}
