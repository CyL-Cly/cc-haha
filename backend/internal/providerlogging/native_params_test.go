package providerlogging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeActualParamsMatchEighteenSourceLoggingContracts(t *testing.T) {
	raw, err := os.ReadFile("testdata/native_query_logging_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct {
		Scenario struct {
			Name           string
			HistorySources []string
			Env            map[string]string
			Options        json.RawMessage
			Thinking       *requestcontext.Thinking
			SDKBetas       []string
		}
		Observation struct {
			Log struct {
				EventName string
				Metadata  protocol.JSONObject
				Keys      []string
				Async     bool
			}
			Physical json.RawMessage
		}
		SourceOnly struct {
			Consumer, Transport, Runtime string
			Capabilities                 struct{ Blocked []string }
			BeforeFirstClient            struct{ UUIDCalls, NewDateCalls int }
			Trace                        []string
		}
	}
	if err := json.Unmarshal(raw, &corpus); err != nil || len(corpus) != 18 {
		t.Fatalf("actual Source logging corpus: %v len=%d", err, len(corpus))
	}
	for _, record := range corpus {
		t.Run(record.Scenario.Name, func(t *testing.T) {
			if record.SourceOnly.Consumer != "src/services/api/claude.ts1945-1967 and logging.ts171-242" || record.SourceOnly.Transport != "guarded loopback" || record.SourceOnly.Runtime != "v24.3.0" || len(record.SourceOnly.Capabilities.Blocked) != 0 {
				t.Fatal("missing actual Source consumer and capability evidence")
			}
			input := requestcontext.Context{Options: requestcontext.Options{Model: "fixture-model", QuerySource: "sdk", IsNonInteractiveSession: true}, Thinking: requestcontext.Thinking{Type: "disabled"}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:43210"}, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", SDKBetas: record.Scenario.SDKBetas}}
			if len(record.Scenario.Options) > 0 {
				if err := json.Unmarshal(record.Scenario.Options, &input.Options); err != nil {
					t.Fatal(err)
				}
			}
			if record.Scenario.Thinking != nil {
				input.Thinking = *record.Scenario.Thinking
			}
			for name, value := range record.Scenario.Env {
				input.Host.Env[name] = value
			}
			for _, source := range record.Scenario.HistorySources {
				var message protocol.JSONObject
				if err := message.UnmarshalJSON([]byte(source)); err != nil {
					t.Fatal(err)
				}
				input.History = append(input.History, message)
			}
			ids, dates := 0, 0
			result, err := requestcontext.Build(input, requestcontext.Dependencies{UUID: func() string { ids++; return fmt.Sprintf("logging-id-%d", ids) }, Now: func() time.Time { dates++; return time.UnixMilli(1700000000000) }})
			if err != nil {
				t.Fatal(err)
			}
			if ids != record.SourceOnly.BeforeFirstClient.UUIDCalls || dates != record.SourceOnly.BeforeFirstClient.NewDateCalls {
				t.Fatalf("normalization side effects differ from actual Source before client: IDs %d dates %d", ids, dates)
			}
			values, err := FromParams(input, result, nil)
			if err != nil {
				t.Fatal(err)
			}
			// The pending permission continuation must not read live params or
			// mutable request options again after synchronous scalar capture.
			input.Host.Env["ANTHROPIC_BASE_URL"] = "changed-after-capture"
			result.Params.SetProperty("thinking", nil)
			result.Params.SetProperty("output_config", nil)
			owner := cooperative.New()
			defer owner.Close()
			queue, _ := New(owner)
			if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if err := queue.LogQuery(scope, values, data("default")); err != nil {
					return err
				}
				events, err := queue.OriginalEvents(scope)
				if err != nil || len(events) != 1 {
					return fmt.Errorf("original query log: %v len=%d", err, len(events))
				}
				event := events[0]
				keys := []string{}
				for _, property := range event.Metadata.OwnProperties() {
					var key string
					_ = json.Unmarshal(property.NameJSON, &key)
					keys = append(keys, key)
				}
				if event.Name != record.Observation.Log.EventName || event.Async != record.Observation.Log.Async || !reflect.DeepEqual(keys, record.Observation.Log.Keys) {
					return fmt.Errorf("Source event/own properties differ: %s %v", event.Name, keys)
				}
				event.Metadata.Set("baseUrl", data("<loopback>"))
				actual, _ := json.Marshal(event.Metadata)
				want, _ := json.Marshal(record.Observation.Log.Metadata)
				actual, _ = protocol.ParseAndStringifyJavaScriptJSON(actual)
				want, _ = protocol.ParseAndStringifyJavaScriptJSON(want)
				if !bytes.Equal(actual, want) {
					return fmt.Errorf("actual native log differs from Source\nwant %s\ngot %s", want, actual)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var body protocol.JSONObject
			if err := body.UnmarshalJSON(result.PreparedRequest.Body); err != nil {
				t.Fatal(err)
			}
			policy := protocol.NewJSONObject()
			for _, name := range []string{"model", "thinking", "temperature", "output_config"} {
				if raw := body.Get(name); raw != nil {
					policy.Set(name, raw)
				}
			}
			var messages []json.RawMessage
			_ = json.Unmarshal(body.Get("messages"), &messages)
			var betas any
			if result.PreparedRequest.Headers.Values("anthropic-beta") != nil {
				betas = result.PreparedRequest.Headers.Get("anthropic-beta")
			}
			actual, _ := json.Marshal(struct {
				Policy         protocol.JSONObject `json:"policy"`
				MessagesLength int                 `json:"messagesLength"`
				Betas          any                 `json:"betas"`
			}{policy, len(messages), betas})
			actual, _ = protocol.ParseAndStringifyJavaScriptJSON(actual)
			want, _ := protocol.ParseAndStringifyJavaScriptJSON(record.Observation.Physical)
			if !bytes.Equal(actual, want) {
				t.Fatalf("actual independently built request differs from Source\nwant %s\ngot %s", want, actual)
			}
		})
	}
}

func TestNativeParamsRequireActualResultAndKeepFiniteGuard(t *testing.T) {
	if _, err := FromParams(requestcontext.Context{}, nil, nil); err == nil {
		t.Fatal("unbound params admitted")
	}
	if _, err := FromOriginal(requestcontext.Context{Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile}, Options: requestcontext.Options{Model: "fixture[1m]"}}, nil); err == nil {
		t.Fatal("old finite context beta guard weakened")
	}
}
