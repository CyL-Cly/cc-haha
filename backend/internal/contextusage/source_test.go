package contextusage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
)

func sourceObject(s string) protocol.JSONObject { return object(json.RawMessage(s)) }
func TestContextAnalyserMatchesActualSourceCategoriesMessagesCanonicalUsageAndGrid(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun not configured")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "source-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	inputs := []Input{}
	for _, profile := range []string{"empty", "messages", "tool-pair", "placeholder", "usage-tail", "compact", "media-low-trust", "narrow-grid", "small-window", "agents", "empty-system", "custom-append"} {
		history := []protocol.JSONObject{}
		prompt := "# Actual custom system\nNative context inspection."
		appendPrompt := ""
		model := "deepseek-flash[1m]"
		window, effective := 1000000.0, 980000.0
		width := 0
		agents := []Agent{}
		if profile != "empty" && profile != "empty-system" {
			history = append(history, sourceObject(`{"type":"user","uuid":"u1","message":{"role":"user","content":"Actual context conversation text."}}`))
		}
		switch profile {
		case "messages":
			history = append(history, sourceObject(`{"type":"assistant","uuid":"a1","message":{"role":"assistant","model":"deepseek-flash","content":[{"type":"thinking","thinking":"actual analysis"},{"type":"text","text":"actual assistant response"}]}}`))
		case "tool-pair":
			history = append(history, sourceObject(`{"type":"assistant","uuid":"a1","message":{"role":"assistant","model":"deepseek-flash","content":[{"type":"tool_use","id":"call-1","name":"Read","input":{"file_path":"actual.txt"}}]}}`), sourceObject(`{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"Actual physical result."}]}}`))
		case "placeholder":
			history = append(history, sourceObject(`{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"No response requested."}],"usage":{"input_tokens":100000,"output_tokens":1}}}`))
		case "usage-tail":
			history = append(history, sourceObject(`{"type":"assistant","message":{"role":"assistant","model":"deepseek-flash","content":[{"type":"text","text":"Provider anchored text"}],"usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":1200,"cache_creation_input_tokens":40}}}`), sourceObject(`{"type":"user","message":{"role":"user","content":"Actual unreported follow-up after the provider anchor."}}`))
		case "compact":
			history = append(history, sourceObject(`{"type":"system","subtype":"compact_boundary"}`), sourceObject(`{"type":"user","message":{"role":"user","content":"Actual retained compacted summary"}}`))
		case "media-low-trust":
			history = append(history, sourceObject(`{"type":"user","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"eA=="}}]}}`), sourceObject(`{"type":"assistant","message":{"role":"assistant","model":"deepseek-flash","content":[{"type":"text","text":"media usage"}],"usage":{"input_tokens":2000000,"output_tokens":1}}}`))
		case "narrow-grid":
			width = 60
		case "small-window":
			model = "context-fixture-small"
			window = 16000
			effective = 12000
			width = 50
		case "agents":
			agents = []Agent{{"actual-configured", "Read the actual private project", "projectSettings"}, {"built-in", "Excluded native builtin", "built-in"}}
		case "empty-system":
			prompt = ""
		case "custom-append":
			appendPrompt = "Actual append system prompt."
		}
		projection, err := preflightcore.AfterCompactBoundary(history)
		if err != nil {
			t.Fatal(err)
		}
		view := []protocol.JSONObject{}
		for _, entry := range projection.Records {
			view = append(view, entry.Record)
		}
		dependencies := requestcontext.Dependencies{UUID: func() string { return "readonly-inspection-uuid" }, Now: func() time.Time { return time.Unix(0, 0) }}
		normalized, err := requestcontext.NormalizeForInspection(view, dependencies)
		if err != nil {
			t.Fatal(profile, err)
		}
		factory, err := messages.NewFactory(dependencies.UUID, dependencies.Now)
		if err != nil {
			t.Fatal(err)
		}
		promptParts := []json.RawMessage{quote(prompt)}
		if profile == "empty-system" {
			// Source's analyser always loads the real default prompt, and an
			// empty custom string chooses it. Reuse its native producer here.
			environment, err := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
			if err != nil {
				t.Fatal(err)
			}
			producer, err := systemprompt.New(systemprompt.Options{Environment: environment, CWD: func() string { return home }, Model: model, Platform: "darwin", OSInfo: func() (systemprompt.OSInfo, error) { return systemprompt.OSInfo{}, nil }, IsGit: func(context.Context) (bool, error) { return false, nil }, InitialSettings: func() (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, SessionStartDate: func() json.RawMessage { return quote("2026-10-07") }, NonInteractive: func() bool { return true }, SkillCommands: func(context.Context, string) ([]systemprompt.Command, error) { return nil, nil }, OutputStyle: func(context.Context, string) (*systemprompt.OutputStyle, error) { return nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			promptParts, err = producer.Resolve(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
		}
		if appendPrompt != "" {
			promptParts = append(promptParts, quote(appendPrompt))
		}
		in := Input{Model: model, Window: window, EffectiveWindow: effective, TerminalWidth: width, AutoCompact: true, Simple: true, LowTrust: true, EstimateOnly: true, History: view, Normalized: normalized, SystemPrompt: promptParts, SystemContext: protocol.NewJSONObject(), Definitions: nil, Schemas: nil, Agents: agents, AttachmentMessages: func(raw json.RawMessage) ([]protocol.JSONObject, error) {
			return attachments.ProjectWithOptions(raw, factory, nil)
		}, EstimateCanonical: func(history []protocol.JSONObject) (float64, error) {
			return preflightcore.EstimateContextWithAttachments(history, &preflightcore.AttachmentOptions{Factory: factory})
		}}
		inputs = append(inputs, in)
		sourceAgents := []map[string]any{}
		for _, agent := range agents {
			sourceAgents = append(sourceAgents, map[string]any{"agentType": agent.Name, "whenToUse": agent.Description, "source": agent.Source})
		}
		rows = append(rows, map[string]any{"profile": profile, "messages": history, "model": model, "width": width, "prompt": prompt, "append": appendPrompt, "agents": sourceAgents})
	}
	estimates := []map[string]any{{"messages": []any{}, "schemas": []any{}}, {"messages": []any{map[string]any{"role": "user", "content": "Real UTF16 😀 words"}}, "schemas": []any{map[string]any{"name": "native", "description": "< & > 😀", "input_schema": map[string]any{"type": "object"}}}}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-context-analyser.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_CONTEXT_ANALYSER_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows, "estimates": estimates, "markdown": true, "renderGrid": true, "environment": map[string]string{"ANTHROPIC_API_KEY": "offline-context-counter-key", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_MODEL": "deepseek-flash[1m]", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_OVERRIDE_DATE": "2026-10-07", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "true", "ENABLE_TOOL_SEARCH": "false", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"deepseek-flash":1000000,"context-fixture-small":16000}`}})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Results   []json.RawMessage
		Estimates []float64
		Markdown  []string
		Rendered  []string
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if len(source.Results) != len(inputs) {
		t.Fatal("Source row count")
	}
	for i, in := range inputs {
		t.Run(rows[i]["profile"].(string), func(t *testing.T) {
			got, err := Analyze(in)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(got)
			var left, right any
			_ = json.Unmarshal(raw, &left)
			_ = json.Unmarshal(source.Results[i], &right)
			if !equalJSON(left, right) {
				t.Fatalf("analyser mismatch: %s", firstJSONDifference(left, right, ""))
			}
			if len(source.Rendered) > i && rows[i]["profile"] == "empty" {
				t.Logf("actual Source grid: %q", source.Rendered[i])
			}
			if text := Markdown(got, false); text != source.Markdown[i] {
				t.Fatalf("actual Source markdown differs: Go %q Source %q", text, source.Markdown[i])
			}
		})
	}
	for i, row := range estimates {
		raw, _ := json.Marshal(row["messages"])
		var messages []protocol.JSONObject
		_ = json.Unmarshal(raw, &messages)
		raw, _ = json.Marshal(row["schemas"])
		var schemas []protocol.JSONObject
		_ = json.Unmarshal(raw, &schemas)
		got, err := preflightcore.RoughAPIRequest(messages, schemas)
		if err != nil || got != source.Estimates[i] {
			t.Fatal("actual rough API request mismatch", i, got, source.Estimates[i], err)
		}
	}
}

func equalJSON(a, b any) bool {
	encodedA, _ := json.Marshal(a)
	encodedB, _ := json.Marshal(b)
	return bytes.Equal(encodedA, encodedB)
}

func firstJSONDifference(a, b any, path string) string {
	switch left := a.(type) {
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok {
			return path + ": object shape mismatch"
		}
		for key, value := range left {
			other, ok := right[key]
			if !ok {
				return path + "/" + key + ": missing Source key"
			}
			if !equalJSON(value, other) {
				return firstJSONDifference(value, other, path+"/"+key)
			}
		}
		for key := range right {
			if _, ok := left[key]; !ok {
				return path + "/" + key + ": missing Go key"
			}
		}
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return path + ": array shape mismatch"
		}
		for i, value := range left {
			if !equalJSON(value, right[i]) {
				return firstJSONDifference(value, right[i], fmt.Sprintf("%s/%d", path, i))
			}
		}
	default:
		return fmt.Sprintf("%s: Go %v Source %v", path, a, b)
	}
	return path + ": mismatch"
}
