package modelyield

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestNativeNonstreamFallbackMatchesActualMainRequestsAndLedger(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	text := strings.Split(queryWire("normal"), "event: message_delta")[0]
	textAndUsage := strings.Split(queryWire("normal"), "event: message_stop")[0]
	apiError := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"temporary\"}}\n\n"
	invalid := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"stream only failed\"}}\n\n"
	for _, row := range []struct {
		name, prefix             string
		status                   int
		enabled, omitUsage, stop bool
		fallbackStatus, retries  int
		stopAssistant            bool
		sharedBudget             bool
	}{
		{name: "empty stream", status: 200, enabled: true},
		{name: "missing fallback usage", status: 200, enabled: true, omitUsage: true},
		{name: "ordinary buffered API fault", prefix: text + apiError, status: 200, enabled: true},
		{name: "nonretryable SSE failure", prefix: invalid, status: 200, enabled: true},
		{name: "404 creation recovers even disabled", prefix: `{"error":{"type":"not_found_error","message":"stream endpoint missing"}}`, status: 404},
		{name: "stop at fallback status", status: 200, enabled: true, stop: true},
		{name: "fallback HTTP authentication error", status: 200, enabled: true, fallbackStatus: 401},
		{name: "fallback HTTP transient exhaustion", status: 200, enabled: true, fallbackStatus: 503},
		{name: "fallback own retry then success", status: 200, enabled: true, fallbackStatus: 503, retries: 1},
		{name: "return at fallback assistant charges finally", status: 200, enabled: true, stopAssistant: true},
		{name: "stream usage and fallback are two charges", prefix: textAndUsage + apiError, status: 200, enabled: true},
		{name: "ordinary fallback respects exhausted compact budget", status: 200, enabled: true, sharedBudget: true},
		{name: "404 creation preserves MAIN budget exception", prefix: `{"error":{"type":"not_found_error","message":"stream endpoint missing"}}`, status: 404, sharedBudget: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			var mu sync.Mutex
			requests := []json.RawMessage{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, append(json.RawMessage(nil), raw...))
				n := len(requests)
				mu.Unlock()
				w.Header().Set("request-id", fmt.Sprintf("fallback-%d", n))
				w.Header().Set("date", "Tue, 14 Nov 2023 22:13:20 GMT")
				if n == 1 {
					w.Header().Set("content-type", "text/event-stream")
					w.WriteHeader(row.status)
					fmt.Fprint(w, row.prefix)
					return
				}
				w.Header().Set("content-type", "application/json")
				if n == 2 && row.fallbackStatus != 0 {
					w.WriteHeader(row.fallbackStatus)
					fmt.Fprint(w, `{"error":{"type":"api_error","message":"fallback rejected"}}`)
					return
				}
				usage := `,"usage":{"input_tokens":8,"output_tokens":3,"service_tier":"priority","inference_geo":"us"}`
				if row.omitUsage {
					usage = ""
				}
				fmt.Fprint(w, `{"id":"actual-fallback","type":"message","role":"assistant","model":"fixture-model","content":[{"type":"text","text":"fallback completed"}],"stop_reason":"end_turn","stop_sequence":null`+usage+`}`)
			}))
			defer peer.Close()
			home := t.TempDir()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-stream-retry.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_STREAM_RETRY_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
			payloadFields := map[string]any{"home": home, "origin": peer.URL, "retries": "0", "maxRetries": fmt.Sprint(row.retries), "fallback": row.enabled, "stopRecovery": row.stop, "stopAssistant": row.stopAssistant}
			if row.sharedBudget {
				payloadFields["budget"] = 1
				payloadFields["compact"] = true
			}
			payload, _ := json.Marshal(payloadFields)
			cmd.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			raw, err := cmd.Output()
			if err != nil {
				t.Fatalf("actual MAIN %v %s", err, stderr.String())
			}
			var source struct {
				Posts        int
				Yielded      []map[string]any
				Totals       struct{ Input, Output float64 }
				Capabilities struct{ Blocked []string }
			}
			if err = json.Unmarshal(raw, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
				t.Fatalf("Source %v %s", err, raw)
			}
			mu.Lock()
			originalRequests := requests
			requests = []json.RawMessage{}
			mu.Unlock()
			if source.Posts != len(originalRequests) {
				t.Fatal("Source physical fetch count lost fallback", source.Posts, len(originalRequests))
			}
			owner := cooperative.New()
			defer owner.Close()
			session, _ := modelaccounting.NewOwnedSession(owner, nil)
			binding, _ := modelaccounting.NewMainContext(owner)
			input := queuedInput(peer.URL)
			input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
			input.Context.Host.Env["CLAUDE_CODE_MAX_RETRIES"] = fmt.Sprint(row.retries)
			disable := "1"
			if row.enabled {
				disable = "0"
			}
			policy := PreserveNativeStreamErrorPolicyOriginalEnvironment(map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": disable, "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "0", "CLAUDE_ENABLE_STREAM_WATCHDOG": "0"})
			yielded := []map[string]any{}
			var totalInput, totalOutput float64
			err = owner.Run(ctx, func(scope *cooperative.Scope) error {
				requestDeps := requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "private-go-id" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error {
					if row.retries == 0 {
						return fmt.Errorf("unexpected retry wait")
					}
					return nil
				}}
				if row.sharedBudget {
					input.Context.Options.QuerySource = "compact"
					requestDeps.NativeCompactSource, _ = cooperative.NewNativeCompactModels(scope, owner)
					requestDeps.APIAttemptBudget = requestretry.NewCompactAttemptBudget(map[string]string{"CLAUDE_CODE_MAX_RETRIES": "0"})
				}
				_, err := Run(ctx, input, Dependencies{Session: session, AgentContext: binding, StreamPolicy: policy, Request: requestDeps, Emit: func(value Yield) (requestretry.NoticeAction, error) {
					var raw []byte
					var err error
					if value.Control != nil {
						raw, err = value.Control.MarshalJSON()
					} else if value.Notice != nil {
						var notice protocol.JSONObject
						notice, err = value.Notice.Envelope()
						if err == nil {
							raw, err = notice.MarshalJSON()
						}
					} else if value.Update != nil && value.Update.Envelope != nil {
						raw, err = value.Update.Envelope.MarshalJSON()
					} else if value.Update != nil {
						raw, err = value.Update.EventJSON()
					} else {
						return 0, fmt.Errorf("unexpected fallback notice")
					}
					if err != nil {
						return 0, err
					}
					var record map[string]any
					if err = json.Unmarshal(raw, &record); err != nil {
						return 0, err
					}
					yielded = append(yielded, record)
					if row.stop && value.Control != nil {
						return requestretry.Stop, nil
					}
					if row.stopAssistant && record["type"] == "assistant" {
						return requestretry.Stop, nil
					}
					return requestretry.Resume, nil
				}})
				if err != nil {
					return err
				}
				snapshot, err := session.Snapshot(scope)
				if err != nil {
					return err
				}
				_ = json.Unmarshal(snapshot.Get("totalInputTokens"), &totalInput)
				_ = json.Unmarshal(snapshot.Get("totalOutputTokens"), &totalOutput)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			actualRequests := requests
			mu.Unlock()
			if len(actualRequests) != source.Posts || !reflect.DeepEqual(yielded, source.Yielded) || totalInput != source.Totals.Input || totalOutput != source.Totals.Output {
				got, _ := json.Marshal(yielded)
				want, _ := json.Marshal(source.Yielded)
				t.Fatalf("fallback differs posts%d/%d usage%v,%v/%v,%v\ngot%s\nwant%s", len(actualRequests), source.Posts, totalInput, totalOutput, source.Totals.Input, source.Totals.Output, got, want)
			}
			for i, actual := range actualRequests {
				var got, want any
				_ = json.Unmarshal(actual, &got)
				_ = json.Unmarshal(originalRequests[i], &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("physical request%d differs\nGo%s\nMAIN%s", i, actual, originalRequests[i])
				}
			}
		})
	}
}
