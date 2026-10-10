package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestNative1MActualQueryLoggingPermissionReactionAndPhysicalRequest(t *testing.T) {
	for _, mode := range []string{"normal", "held", "permission-getter-error", "params-callback-error"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			bodies := make(chan json.RawMessage, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				body, _ := io.ReadAll(r.Body)
				bodies <- body
				w.Header().Set("content-type", "text/event-stream")
				w.Header().Set("request-id", "native-original-request")
				_, _ = fmt.Fprint(w, queryWire("normal"))
			}))
			defer server.Close()
			owner := cooperative.New()
			defer owner.Close()
			session, _ := modelaccounting.NewOwnedSession(owner, nil)
			binding, _ := modelaccounting.NewMainContext(owner)
			cause := errors.New("original pre-client logging failure")
			input := queuedInput(server.URL)
			input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
			input.Context.Options.Model = "deepseek-v4-flash[1m]"
			input.Context.Options.EffortValue = json.RawMessage(`"max"`)
			input.Context.Host.Env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "deepseek-v4-flash"
			input.Context.Host.Env["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "thinking,adaptive_thinking,effort,max_effort"
			input.Context.Thinking.Type = "adaptive"
			var permission *ownerasync.OwnerFuture[protocol.JSONObject]
			gets, clients, captures, dates := 0, 0, 0, 0
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				originalEmptySettings := func() (*protocol.JSONObject, error) { empty := protocol.NewJSONObject(); return &empty, nil }
				if err := session.InstallNativeCalculator(scope, func(model string, environment map[string]string) (*modelaccounting.Calculator, error) {
					return modelaccounting.NewNativeCalculator(model, modelaccounting.NativeCalculatorOptions{Environment: environment, Settings: originalEmptySettings, Global: originalEmptySettings})
				}); err != nil {
					return err
				}
				permission, _ = ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
				modeValue := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: json.RawMessage(`"default"`)})
				if mode != "held" {
					if err := permission.Resolve(scope, modeValue); err != nil {
						return err
					}
				}
				deps := Dependencies{Session: session, AgentContext: binding, Emit: func(Yield) (requestretry.NoticeAction, error) { return requestretry.Resume, nil }}
				deps.Request = requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "native-original-uuid" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("unexpected retry") }}
				deps.Request.OnParamsPrepared = func(result *requestcontext.Result) error {
					captures++
					if clients != 0 || dates != 2 || result.Params.Property("thinking").Property("type") == nil {
						return errors.New("native params were not captured after original two clocks and before client")
					}
					if mode == "params-callback-error" {
						return cause
					}
					return nil
				}
				deps.Request.OnClient = func() {
					clients++
					if gets != 1 || dates != 2 || captures != 1 {
						t.Error("first client preceded actual query permission getter/params or changed duration clocks")
					}
				}
				deps.SuccessDuration = &modelaccounting.SuccessDuration{NowMillis: func() float64 { dates++; return float64(100 + dates) }, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
					gets++
					if gets == 1 && (clients != 0 || dates != 2 || captures != 1) {
						return nil, errors.New("Source pre-client logging permission order changed")
					}
					if mode == "permission-getter-error" {
						return nil, cause
					}
					return permission, nil
				}}
				_, err := Run(context.Background(), input, deps)
				if mode == "permission-getter-error" || mode == "params-callback-error" {
					if err != cause || clients != 0 || requests.Load() != 0 {
						return fmt.Errorf("original pre-query failure became model terminal or crossed client: %v clients%d POSTs%d", err, clients, requests.Load())
					}
					return nil
				}
				if err != nil {
					return err
				}
				if requests.Load() != 1 || captures != 1 || clients != 1 || gets != 2 {
					return fmt.Errorf("physical native producer/logging lifecycle: POSTs%d params%d clients%d permission%d", requests.Load(), captures, clients, gets)
				}
				if mode == "held" {
					logs, err := session.QueryLogSnapshot(scope)
					if err != nil || len(logs) != 0 {
						return fmt.Errorf("held permission log was drained or synthetic: %v len%d", err, len(logs))
					}
					// Original continuation data is captured before this later host
					// mutation, while permission mode is read at reaction execution.
					input.Context.Options.Model = "changed-after-request"
					input.Context.Thinking.Type = "disabled"
					input.Context.Options.EffortValue = json.RawMessage(`"low"`)
					modeValue.Set("mode", json.RawMessage(`"acceptEdits"`))
					if err := permission.Resolve(scope, modeValue); err != nil {
						return err
					}
				}
				if _, err := permission.Await(scope); err != nil {
					return err
				}
				logs, err := session.QueryLogSnapshot(scope)
				if err != nil || len(logs) != 1 {
					return fmt.Errorf("actual owner query log: %v len%d", err, len(logs))
				}
				var metadata protocol.JSONObject
				_ = metadata.UnmarshalJSON(logs[0].Get("metadata"))
				wantMode := "default"
				if mode == "held" {
					wantMode = "acceptEdits"
				}
				if metadata.String("model") != "deepseek-v4-flash[1m]" || metadata.String("thinkingType") != "adaptive" || metadata.String("effortValue") != "max" || metadata.String("betas") != "context-1m-2025-08-07,effort-2025-11-24" || metadata.String("permissionMode") != wantMode {
					return fmt.Errorf("actual params/late permission capture changed: %s", logs[0].Get("metadata"))
				}
				var body protocol.JSONObject
				_ = body.UnmarshalJSON(<-bodies)
				if body.String("model") != "deepseek-v4-flash" || string(body.Get("thinking")) != `{"type":"adaptive"}` || string(body.Get("output_config")) != `{"effort":"max"}` {
					return fmt.Errorf("physical first Result differs from logged Native 1m params: %s", body.Get("model"))
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
