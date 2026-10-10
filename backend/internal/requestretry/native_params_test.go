package requestretry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeFirstActualParamsPrecedeClientAndRetryUsesOnePreparation(t *testing.T) {
	server := startFixtureServer(t, "overflow", nil)
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	input.Context.Options.Model = "fixture-model[1m]"
	input.Context.History = append(input.Context.History, originalHistoryRecord(t, `{"type":"assistant","uuid":"a","message":{"id":"a","model":"fixture-model","role":"assistant","content":[{"type":"tool_use","id":"missing","name":"UnknownTool","input":{}}],"stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":0}}}`))
	trace := []string{}
	ids, dates, preparations, paramsCalls := 0, 0, 0, 0
	deps.UUID = func() string { ids++; trace = append(trace, "uuid"); return "native-params-id" }
	deps.Now = func() time.Time { dates++; trace = append(trace, "date"); return time.UnixMilli(1700000000000) }
	deps.OnPrepared = func(history []protocol.JSONObject) error {
		preparations++
		trace = append(trace, "prepared")
		if len(history) != 3 || ids != 1 || dates != 1 {
			return errors.New("Source repair was not completed exactly once before params/client")
		}
		return nil
	}
	var firstBody json.RawMessage
	deps.OnParamsPrepared = func(result *requestcontext.Result) error {
		paramsCalls++
		trace = append(trace, "params")
		firstBody = append(json.RawMessage(nil), result.PreparedRequest.Body...)
		if server.requests.Load() != 0 || !strings.Contains(result.PreparedRequest.Headers.Get("anthropic-beta"), "context-1m-2025-08-07") {
			return errors.New("actual first params were not available before physical request")
		}
		return nil
	}
	deps.OnKeyLookup = func() { trace = append(trace, "key") }
	deps.OnClient = func() { trace = append(trace, "client") }
	deps.OnAttempt = func(float64) { trace = append(trace, "attempt") }
	stream, err := Open(context.Background(), input, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		_, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if paramsCalls != 1 || preparations != 1 || ids != 1 || dates != 1 || server.requests.Load() != 2 {
		t.Fatalf("extra params/preparation side effects: params %d prepare %d IDs %d dates %d POSTs %d", paramsCalls, preparations, ids, dates, server.requests.Load())
	}
	if !reflect.DeepEqual(trace[:7], []string{"uuid", "date", "prepared", "params", "key", "client", "attempt"}) {
		t.Fatalf("actual params/client order %v", trace)
	}
	actualFirst, second := <-server.bodies, <-server.bodies
	if actualFirst != string(firstBody) || actualFirst == second {
		t.Fatalf("first Result/retry-sensitive max_tokens reuse differs\nfirst %s\nsecond %s", actualFirst, second)
	}
}

func TestNativeParamsFailureHasOriginalPriorityBeforeClientAndFiniteRejectsHook(t *testing.T) {
	for _, profile := range []string{requestcontext.NativeDirectRelayProfile, requestcontext.DirectRelayProfile} {
		t.Run(profile, func(t *testing.T) {
			input, deps := standardInput(), standardDeps()
			input.Context.Host.Profile = profile
			cause := errors.New("original params consumer failure")
			clients, keys, calls := 0, 0, 0
			deps.OnClient = func() { clients++ }
			deps.OnKeyLookup = func() { keys++ }
			deps.OnParamsPrepared = func(*requestcontext.Result) error { calls++; return cause }
			_, err := Open(context.Background(), input, deps)
			if clients != 0 || keys != 0 || err == nil {
				t.Fatalf("error crossed first client boundary: %v clients%d keys%d", err, clients, keys)
			}
			if profile == requestcontext.NativeDirectRelayProfile && (err != cause || calls != 1) || profile == requestcontext.DirectRelayProfile && (err == cause || calls != 0) {
				t.Fatalf("native callback/finite capability priority: %v calls%d", err, calls)
			}
		})
	}
}
