package sdkhost

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// These are actual guarded Source isResultSuccessful/SYNTHETIC_MESSAGES helper
// observations. This proves result extraction branches, not a full SDK ASK,
// nor the unported process-wide execution error ring-buffer watermark.
func TestActualSourceResultHelperProjection(t *testing.T) {
	data, e := os.ReadFile("testdata/result_helpers.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Original struct {
			Message json.RawMessage
			Stop    json.RawMessage
		}
		Expected struct {
			Success    bool
			Text       string
			IsApiError bool
		}
	}
	if e = json.Unmarshal(data, &cases); e != nil {
		t.Fatal(e)
	}
	for index, item := range cases {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			calls := 0
			h := Host{options: Options{UUID: func() string { calls++; return "original-result-id" }, NowMillis: func() float64 { return 1700000000000 }}}
			accounting := &Accounting{models: protocol.NewJSONObject()}
			var record protocol.JSONObject
			if e = record.UnmarshalJSON(item.Original.Message); e != nil {
				t.Fatal(e)
			}
			got, e := projectResult(queryloop.Message{Record: record}, zeroUsage(), item.Original.Stop, 1700000000000, "original-helper-SID", accounting, 0, h.options.NowMillis, h.options.UUID)
			if e != nil {
				t.Fatal(e)
			}
			if (got.String("subtype") == "success") != item.Expected.Success || got.String("result") != item.Expected.Text {
				t.Fatalf("Source result mismatch: %s", raw(got))
			}
			if item.Expected.Success && string(got.Get("is_error")) != string(raw(item.Expected.IsApiError)) {
				t.Fatalf("Source API error truth mismatch: %s", raw(got))
			}
			if calls != 1 || got.String("uuid") != "original-result-id" {
				t.Fatal("result UUID allocation changed")
			}
			if !item.Expected.Success && !strings.Contains(string(got.Get("errors")), "[ede_diagnostic]") {
				t.Fatal("execution diagnostic prefix missing")
			}
		})
	}
}
func TestUsageFutureAndInvalidCapability(t *testing.T) {
	for _, original := range []json.RawMessage{json.RawMessage(`{"future_metric":1}`), json.RawMessage(`{"input_tokens":"5"}`), json.RawMessage(`{"server_tool_use":{"web_search_requests":"2"}}`)} {
		before := zeroUsage()
		snapshot, _ := before.MarshalJSON()
		_, err := updateUsage(before, object(original))
		if err == nil {
			t.Fatalf("unsupported original usage escaped finite profile: %s", original)
		}
		after, _ := before.MarshalJSON()
		if string(snapshot) != string(after) {
			t.Fatal("invalid update mutated usage before refusal")
		}
	}
	if _, err := updateUsage(zeroUsage(), protocol.JSONObject{}); err != nil {
		t.Fatal(err)
	}
	a, e := newAccounting("fixture-model", map[string]string{"ANTHROPIC_BASE_URL": "https://127.0.0.1:1", "ANTHROPIC_API_KEY": "offline", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"})
	if e != nil {
		t.Fatal(e)
	}
	bad := zeroUsage()
	bad.Set("input_tokens", json.RawMessage("-1"))
	if _, e = a.charge(bad); e == nil || a.total != 0 || len(a.models.OwnProperties()) != 0 {
		t.Fatal("invalid usage charged session")
	}
}
func TestRetryTimerUsesOwnerAndIndependentModelCancel(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	h := Host{}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		deps := h.requestDependencies(scope)
		if e := deps.Sleep(context.Background(), 1); e != nil {
			return e
		}
		model, cancel := context.WithCancel(context.Background())
		cancel()
		if e := deps.Sleep(model, 1000); e != context.Canceled {
			t.Fatalf("model cancel lost: %v", e)
		}
		_, e := deps.BodyIOAwait(provider.OwnerBodyRead, func(context.Context) ([]byte, error) { return []byte("immutable physical completion"), nil })
		if e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	closed, cancel := context.WithCancel(context.Background())
	cancel()
	if e := owner.Run(closed, func(scope *cooperative.Scope) error {
		return h.requestDependencies(scope).Sleep(context.Background(), 1)
	}); e != context.Canceled {
		t.Fatal("cancelled lifecycle admitted")
	}
	_ = time.Second
}

func TestMalformedProgrammaticEnvelopeDoesNotPublishResult(t *testing.T) {
	record := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("assistant")}, protocol.JSONField{Name: "message", Value: json.RawMessage(`{"role":"assistant","content":`)})
	if _, e := projectResult(queryloop.Message{Record: record}, zeroUsage(), json.RawMessage("null"), 0, "original", &Accounting{models: protocol.NewJSONObject()}, 0, func() float64 { return 0 }, func() string { t.Fatal("invalid envelope allocated result ID"); return "" }); e == nil {
		t.Fatal("invalid programmatic envelope swallowed codec error")
	}
	if _, e := jsonBytes(record); e == nil {
		t.Fatal("invalid FIFO input swallowed codec error")
	}
}
