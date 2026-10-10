package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func TestActualSourceRawToolIdentities(t *testing.T) {
	raw, err := os.ReadFile("testdata/identity_source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Scenario    identityFixtureInput
			Observation json.RawMessage
			SourceOnly  json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &corpus); err != nil || len(corpus.Cases) != 22 {
		t.Fatalf("actual Source identity corpus %v %d", err, len(corpus.Cases))
	}
	for _, row := range corpus.Cases {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			actual := runIdentityFixture(t, row.Scenario, t.TempDir())
			var value struct {
				Observation json.RawMessage
				SourceOnly  json.RawMessage
			}
			if err := json.Unmarshal(actual, &value); err != nil {
				t.Fatal(err)
			}
			for index, pair := range [][2]json.RawMessage{{row.Observation, value.Observation}, {row.SourceOnly, value.SourceOnly}} {
				var want, got any
				if json.Unmarshal(pair[0], &want) != nil || json.Unmarshal(pair[1], &got) != nil || !reflect.DeepEqual(want, got) {
					t.Fatalf("Source identity projection %d differs\nSource:%s\nGo:%s", index, pair[0], pair[1])
				}
			}
		})
	}
}

func jsonCapabilities() *JSONToolBatchCapabilities {
	return &JSONToolBatchCapabilities{
		PreTool: func(_ context.Context, _ RegisteredTool, value *protocol.JSONObject, _ JSONToolCallContext) (*protocol.JSONObject, error) {
			return value, nil
		},
		ResolvePermission: func(_ context.Context, _ RegisteredTool, value *protocol.JSONObject, _ JSONToolCallContext) (ToolPermission, error) {
			return ToolPermission{Behavior: "allow", Input: value}, nil
		},
		PostTool: func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, JSONToolCallContext) ([]Message, error) {
			return nil, nil
		},
		InProgress: func(json.RawMessage, bool) error { return nil },
	}
}

func TestJSONCapabilitiesAreRequiredAndRegistryDeclarationsRemainScalar(t *testing.T) {
	for _, mutate := range []func(*batchHarness){
		func(h *batchHarness) { h.Capabilities.JSON = &JSONToolBatchCapabilities{} },
		func(h *batchHarness) { h.Capabilities.JSON = jsonCapabilities(); h.Capabilities.JSON.PreTool = nil },
		func(h *batchHarness) {
			h.Capabilities.JSON = jsonCapabilities()
			h.Capabilities.JSON.ResolvePermission = nil
		},
		func(h *batchHarness) { h.Capabilities.JSON = jsonCapabilities(); h.Capabilities.JSON.PostTool = nil },
		func(h *batchHarness) { h.Capabilities.JSON = jsonCapabilities(); h.Capabilities.JSON.InProgress = nil },
		func(h *batchHarness) { h.Tool.Name = string([]byte{0xff}) },
		func(h *batchHarness) { h.Tool.Aliases = []string{string([]byte{0xff})} },
		func(h *batchHarness) {
			h.Capabilities.DeprecatedCatalog = []ToolDescriptor{{Name: string([]byte{0xff})}}
		},
		func(h *batchHarness) {
			h.Capabilities.DeprecatedCatalog = []ToolDescriptor{{Name: "Write", Implementation: &RegisteredTool{Name: "Write"}}}
		},
		func(h *batchHarness) {
			copy := h.Tool
			h.Capabilities.DeprecatedCatalog = []ToolDescriptor{{Name: "Other", Aliases: copy.Aliases, Implementation: &copy}}
		},
	} {
		h := newBatchHarness(t)
		mutate(h)
		if _, err := h.run(); err == nil || h.Calls != 0 || len(h.Order) != 0 {
			t.Fatalf("invalid capability/declaration executed: %v %v", err, h.Order)
		}
	}
}

func TestDeprecatedLookupPreservesFirstMatchAndAliasOnlyFallback(t *testing.T) {
	for _, name := range []string{"LegacyWrite", "Write", "shared", "unported", "absent"} {
		t.Run(name, func(t *testing.T) {
			h := newBatchHarness(t)
			implementation := h.Tool
			implementation.Aliases = []string{"LegacyWrite"}
			h.Capabilities.DeprecatedCatalog = []ToolDescriptor{{Name: "Write", Aliases: implementation.Aliases, Implementation: &implementation}, {Name: "shared"}, {Name: "Later", Aliases: []string{"shared"}}, {Name: "Unsupported", Aliases: []string{"unported"}}}
			batch, err := NewSerialToolBatch(nil, h.Capabilities, h.Factory)
			if err != nil {
				t.Fatal(err)
			}
			h.Uses[0].Block.Object.Set("name", quoted(name))
			messages := []Message{}
			_, err = batch.Execute(context.Background(), Round{}, h.Uses, func(m Message) error { messages = append(messages, m); return nil })
			if name == "unported" {
				if !errors.Is(err, ErrUnsupported) || h.Calls != 0 || h.UUIDs != 0 {
					t.Fatalf("missing fallback handler executed: %v", err)
				}
				return
			}
			if err != nil || len(messages) != 1 {
				t.Fatalf("fallback result %v %d", err, len(messages))
			}
			if name == "LegacyWrite" {
				if h.Calls != 1 {
					t.Fatal("resolved alias not executed")
				}
			} else {
				raw, _ := messages[0].MarshalJSON()
				if h.Calls != 0 || !strings.Contains(string(raw), "No such tool available") {
					t.Fatalf("primary or later alias bypassed available registry: %s", raw)
				}
			}
		})
	}
	// A descriptor primary and alias may both match, as in Source includes().
	h := newBatchHarness(t)
	h.Tool.Aliases = []string{"Write"}
	batch, err := NewSerialToolBatch(nil, ToolBatchCapabilities{PreTool: h.Capabilities.PreTool, ResolvePermission: h.Capabilities.ResolvePermission, PostTool: h.Capabilities.PostTool, InProgress: h.Capabilities.InProgress, DeprecatedCatalog: []ToolDescriptor{{Name: "Write", Aliases: h.Tool.Aliases, Implementation: &h.Tool}}}, h.Factory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = batch.Execute(context.Background(), Round{}, h.Uses, h.Emit); err != nil || h.Calls != 1 {
		t.Fatalf("primary plus identical alias changed: %v", err)
	}
}

func TestRawCallbackIDsAreImmutableStringsAcrossEveryHostPhase(t *testing.T) {
	h := newBatchHarness(t)
	h.Capabilities.JSON = jsonCapabilities()
	h.Uses[0].Block.Object.Set("id", json.RawMessage(`"\ud800"`))
	want := `"\ud800"`
	seen := []string{}
	mutate := func(call JSONToolCallContext) { seen = append(seen, string(call.ToolUseID)); call.ToolUseID[1] = 'x' }
	h.Capabilities.JSON.InProgress = func(raw json.RawMessage, _ bool) error { seen = append(seen, string(raw)); raw[1] = 'x'; return nil }
	h.Capabilities.JSON.PreTool = func(_ context.Context, _ RegisteredTool, input *protocol.JSONObject, call JSONToolCallContext) (*protocol.JSONObject, error) {
		mutate(call)
		return input, nil
	}
	h.Capabilities.JSON.ResolvePermission = func(_ context.Context, _ RegisteredTool, input *protocol.JSONObject, call JSONToolCallContext) (ToolPermission, error) {
		mutate(call)
		return ToolPermission{Behavior: "allow", Input: input}, nil
	}
	h.Capabilities.JSON.PostTool = func(_ context.Context, _ RegisteredTool, _ *protocol.JSONObject, _ ToolCallResult, call JSONToolCallContext) ([]Message, error) {
		mutate(call)
		return nil, nil
	}
	h.Tool.CallJSON = func(_ context.Context, _ protocol.JSONObject, call JSONToolCallContext) (ToolCallResult, error) {
		h.Calls++
		mutate(call)
		return ToolCallResult{Data: quoted("ok")}, nil
	}
	h.Tool.MapResultJSON = func(data, id json.RawMessage) (protocol.JSONObject, error) {
		seen = append(seen, string(id))
		id[1] = 'x'
		return fixtureObject(`{"type":"tool_result","content":"ok"}`), nil
	}
	if _, err := h.run(); err != nil || h.Calls != 1 || len(seen) != 7 {
		t.Fatalf("raw call %v %v", err, seen)
	}
	for _, value := range seen {
		if value != want {
			t.Fatalf("mutable JSON buffer changed string identity %v", seen)
		}
	}
	if string(h.Uses[0].Block.Object.Get("id")) != want {
		t.Fatal("callback mutated original identity")
	}
}

func TestScalarAdaptersNeverReceiveReplacementFromMixedRawCapabilities(t *testing.T) {
	for _, rawPart := range []string{"hooks only", "call only", "map only"} {
		h := newBatchHarness(t)
		h.Uses[0].Block.Object.Set("id", json.RawMessage(`"\ud800"`))
		if rawPart == "hooks only" {
			h.Capabilities.JSON = jsonCapabilities()
		} else if rawPart == "call only" {
			h.Tool.CallJSON = func(context.Context, protocol.JSONObject, JSONToolCallContext) (ToolCallResult, error) {
				t.Fatal("legacy hook should reject first")
				return ToolCallResult{}, nil
			}
		} else {
			h.Tool.MapResultJSON = func(json.RawMessage, json.RawMessage) (protocol.JSONObject, error) {
				t.Fatal("legacy call should reject first")
				return protocol.JSONObject{}, nil
			}
		}
		if _, err := h.run(); !errors.Is(err, ErrUnsupported) || h.Calls != 0 || len(h.Order) != 0 {
			t.Fatalf("mixed scalar adapter escaped: %s %v %v", rawPart, err, h.Order)
		}
	}
}

func TestUnknownFeedbackAndCanceledToolsRespectConsumerAndProgressOwnership(t *testing.T) {
	sentinel := errors.New("host stop")
	for _, phase := range []string{"add", "emit", "complete", "missing cancellation", "canceled emit", "canceled complete", "marker emit", "marker complete"} {
		h := newBatchHarness(t)
		h.Capabilities.DeprecatedCatalog = []ToolDescriptor{}
		h.Capabilities.CancellationContent = quoted(cancelMessage)
		h.Uses[0].Block.Object.Set("name", json.RawMessage(`"\ud800"`))
		if strings.Contains(phase, "marker") {
			h.Uses[0].Block.Object.Set("input", json.RawMessage(`{"__unparsedToolInput":{"raw":"bad\ud800","len":4}}`))
		}
		if strings.Contains(phase, "cancel") {
			h.Uses[0].Block.Object.Set("name", quoted("Write"))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			h.ctx = ctx
		}
		if phase == "missing cancellation" {
			h.Capabilities.CancellationContent = nil
		}
		h.Capabilities.InProgress = func(_ string, active bool) error {
			if phase == "add" && active || strings.Contains(phase, "complete") && !active {
				return sentinel
			}
			return nil
		}
		h.Emit = func(Message) error {
			if strings.Contains(phase, "emit") {
				return sentinel
			}
			return nil
		}
		_, err := h.run()
		if phase == "missing cancellation" {
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("missing resolved policy passed %v", err)
			}
		} else if err != sentinel {
			t.Fatalf("phase %s changed ownership %v", phase, err)
		}
		if h.Calls != 0 {
			t.Fatal("feedback executed handler")
		}
	}
}

func TestRawObservableLookupRejectsInvalidNamesWithoutCallingBackfill(t *testing.T) {
	h := newBatchHarness(t)
	h.Tool.Backfill = func(*protocol.JSONObject) error { t.Fatal("invalid model name reached backfill"); return nil }
	batch, err := NewSerialToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `3`, `{"future":true}`, `[`} {
		if _, err := batch.LookupBackfillJSON(json.RawMessage(raw)); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("invalid raw lookup name accepted %s %v", raw, err)
		}
	}
	block := h.Uses[0].Block
	block.Object.Set("name", json.RawMessage(`null`))
	_, err = observableEnvelopeJSON(&streamhistory.Envelope{Content: []*streamhistory.Block{block}}, batch.LookupBackfillJSON)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("invalid observable model name accepted %v", err)
	}
}
