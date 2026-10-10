package requestretry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func originalRecord(t *testing.T, raw string) protocol.JSONObject {
	t.Helper()
	var result protocol.JSONObject
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestActualSourceSharedOriginalOpenerContracts(t *testing.T) {
	raw, err := os.ReadFile("testdata/shared_opener_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Bun, Revision, Source string
		Cases                 []struct {
			Input    fixtureInput
			Expected json.RawMessage
		}
	}
	if err = json.Unmarshal(raw, &contract); err != nil || contract.Bun != "1.3.14" || contract.Revision != "0d9b296af33f2b851fcbf4df3e9ec89751734ba4" || len(contract.Cases) != 14 || contract.Source == "" {
		t.Fatal("missing actual original Source contract", err)
	}
	for _, item := range contract.Cases {
		t.Run(item.Input.Name, func(t *testing.T) {
			server := startFixtureServer(t, item.Input.Mode, nil)
			input := item.Input
			input.Env["ANTHROPIC_BASE_URL"] = server.origin
			actual, err := observeFixture(input)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(actual)
			var observed, expected map[string]any
			_ = json.Unmarshal(raw, &observed)
			_ = json.Unmarshal(item.Expected, &expected)
			observed["server"] = server.state()
			if !reflect.DeepEqual(observed, expected) {
				a, _ := json.Marshal(observed)
				t.Fatalf("independent original opener differs\nGo %s\nSource %s", a, item.Expected)
			}
		})
	}
}

func TestOriginalAuthAndTextualToolCapabilitiesBeforeTransport(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Input)
	}{
		{"non-bare interactive", func(i *Input) { i.Context.Options.IsNonInteractiveSession = false }},
		{"unsupported bare value", func(i *Input) { i.Context.Host.Env["CLAUDE_CODE_SIMPLE"] = "true" }},
		{"original flag path", func(i *Input) { i.Auth.FlagSettingsPath = "/private/original-settings.json" }},
		{"original flag inline", func(i *Input) { i.Auth.FlagSettingsInline = json.RawMessage(`{"apiKeyHelper":"must not execute"}`) }},
		{"unsupported empty inline object", func(i *Input) { i.Auth.FlagSettingsInline = json.RawMessage(`{}`) }},
		{"scalar extra schema", func(i *Input) { i.Context.Options.ExtraToolSchemas = []json.RawMessage{json.RawMessage(`3`)} }},
		{"null extra schema", func(i *Input) { i.Context.Options.ExtraToolSchemas = []json.RawMessage{json.RawMessage(`null`)} }},
		{"extra schema empty name", func(i *Input) { i.Context.Options.ExtraToolSchemas = []json.RawMessage{json.RawMessage(`{"name":""}`)} }},
		{"extra schema numeric name", func(i *Input) { i.Context.Options.ExtraToolSchemas = []json.RawMessage{json.RawMessage(`{"name":3}`)} }},
		{"tool choice scalar", func(i *Input) { i.Context.Options.ToolChoice = json.RawMessage(`3`) }},
		{"tool choice null", func(i *Input) { i.Context.Options.ToolChoice = json.RawMessage(`null`) }},
		{"tool choice unknown type", func(i *Input) { i.Context.Options.ToolChoice = json.RawMessage(`{"type":"unknown"}`) }},
		{"tool choice missing name", func(i *Input) { i.Context.Options.ToolChoice = json.RawMessage(`{"type":"tool"}`) }},
		{"tool result invalid id", func(i *Input) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"tool_result","tool_use_id":null,"content":"ok"}]}`))
		}},
		{"tool result scalar content", func(i *Input) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"tool_result","tool_use_id":"call","content":42}]}`))
		}},
		{"tool result object content", func(i *Input) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"tool_result","tool_use_id":"call","content":{}}]}`))
		}},
		{"tool result media", func(i *Input) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"tool_result","tool_use_id":"call","content":[{"type":"image"}]}]}`))
		}},
		{"tool result scalar block", func(i *Input) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"tool_result","tool_use_id":"call","content":[1]}]}`))
		}},
		{"tool result non-string nested text", func(i *Input) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"tool_result","tool_use_id":"call","content":[{"type":"text","text":false}]}]}`))
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			input, deps := standardInput(), standardDeps()
			item.change(&input)
			preparations, clients := 0, 0
			deps.OnPreparation = func([]protocol.JSONObject) { preparations++ }
			deps.OnClient = func() { clients++ }
			_, err := Open(context.Background(), input, deps)
			var capability *CapabilityError
			if !errors.As(err, &capability) || preparations != 0 || clients != 0 {
				t.Fatalf("unsupported original input escaped: %v P%d C%d", err, preparations, clients)
			}
		})
	}
}

func TestBareInputKeepsOriginalStateAndSharedNativeSchemaCache(t *testing.T) {
	server := startFixtureServer(t, "sequence", func(int) (int, string) { return 200, responseWire })
	input, deps := standardInput(), standardDeps()
	input.Context.Options.IsNonInteractiveSession = false
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	input.Context.Host.Env["CLAUDE_CODE_SIMPLE"] = "1"
	input.Auth.FlagSettingsInline = json.RawMessage(` null `)
	input.Context.Tools = []protocol.JSONObject{originalRecord(t, `{"kind":"registered","name":"FixtureWrite","inputSchema":{"kind":"object","unknownKeys":"strip","fields":[]},"prompt":"original prompt","description":"original description"}`)}
	input.Context.Host.ToolSchemas = toolschema.NewSession()
	input.Context.Host.ToolSchemaHost = &toolschema.Host{MainLoopModel: input.Context.Options.Model}
	input.Context.Options.ExtraToolSchemas = []json.RawMessage{json.RawMessage(`{"name":"extra","type":"custom-extra","future":9007199254740993}`)}
	for _, choice := range []string{`{"type":"any"}`, `{"type":"tool","name":"FixtureWrite"}`} {
		input.Context.Options.ToolChoice = json.RawMessage(choice)
		stream, err := Open(context.Background(), input, deps)
		if err != nil {
			t.Fatal(err)
		}
		for {
			_, err := stream.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		body := <-server.bodies
		var request map[string]json.RawMessage
		_ = json.Unmarshal([]byte(body), &request)
		if string(request["tool_choice"]) != choice {
			t.Fatalf("original tool choice altered: %s", body)
		}
		if input.Context.Host.Env["CLAUDE_CODE_SIMPLE"] != "1" || input.Context.Host.ToolSchemas.PromptCalls()["FixtureWrite"] != 1 {
			t.Fatal("caller original host mutated or native prompt cache recreated")
		}
	}
	if server.requests.Load() != 2 {
		t.Fatal("successful stream resubmitted", server.requests.Load())
	}
}
