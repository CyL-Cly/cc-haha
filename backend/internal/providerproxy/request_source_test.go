package providerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestResponsesRequestAndCompatibilityAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	base := requestObject([]byte(`{"model":"gpt-6-sol","messages":[{"role":"user","content":"hello"}],"max_tokens":16,"stream":true}`))
	type row struct {
		Kind    string              `json:"kind"`
		Body    protocol.JSONObject `json:"body"`
		Options protocol.JSONObject `json:"options"`
	}
	rows := []row{}
	contents := []string{`"plain \ud800 text"`, `[]`, `null`, `false`, `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, `[{"type":"text","text":""}]`, `[{"type":"image","source":{"type":"url","url":"https://image.invalid/a"}}]`, `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]`, `[{"type":"image","source":{"type":"file","file_id":"private"}}]`, `[{"type":"document","title":"Doc","context":"Context","source":{"type":"text","data":"body\udfff"}}]`, `[{"type":"document","title":"Doc","context":"Context","source":{"type":"url","url":"https://doc.invalid/a"}}]`, `[{"type":"document","title":"","context":"Context","source":{"type":"file","file_id":"private"}}]`, `[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AAAA"}}]`, `[{"type":"document","title":"Doc","source":{"type":"content","content":"text"}}]`, `[{"type":"document","title":"Doc","source":{"type":"content","content":[{"type":"text","text":"a"},{"type":"image","source":{"type":"url","url":"https://image.invalid/a"}}]}}]`, `[{"type":"search_result","title":"Title","source":"https://search.invalid/","content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}]`, `[{"type":"thinking","thinking":"skipped"}]`, `[{"type":"text","text":"before"},{"type":"tool_use","id":"call-1","name":"Read","input":{"path":"x\ud800.txt","z":2,"a":1}},{"type":"text","text":"after"}]`, `[{"type":"tool_use","id":"call-1","name":"Read","input":"{already raw}"}]`, `[{"type":"tool_use","id":"call-1","name":"Read"}]`, `[{"type":"tool_result","tool_use_id":"call-1","content":"text"}]`, `[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]`, `[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"document","source":{"type":"text","data":"document"}}]}]`, `[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":"a"},{"type":"image","source":{"type":"url","url":"https://image.invalid/a"}}]}]`, `[{"type":"tool_result","tool_use_id":"call-1"}]`, `[{"type":"redacted_thinking","data":"opaque"}]`, `[{"type":"redacted_thinking","data":"cc-haha:openai-reasoning:v1:{\"id\":\"rs-1\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"s\"}],\"encrypted_content\":\"private-fixture\"}"}]`, `[{"type":"redacted_thinking","data":"cc-haha:openai-reasoning:v1:{\"id\":null,\"summary\":[],\"encrypted_content\":\"private-fixture\"}"}]`}
	for _, content := range contents {
		for _, options := range []string{`{}`, `{"openAICodexOAuth":true,"preserveOpenAIReasoning":true,"cacheKey":"stable-session"}`, `{"passSamplingParams":true}`, `{"requestCompatibility":{"sampling":"unsupported","reasoning":"unsupported"}}`} {
			body := base.Clone()
			body.Set("messages", []byte(`[{"role":"user","content":`+content+`},{"role":"assistant","content":`+content+`}]`))
			body.Set("temperature", []byte(`0.2`))
			body.Set("top_p", []byte(`0.9`))
			rows = append(rows, row{"request", body, requestObject([]byte(options))})
		}
	}
	for _, system := range []string{`"x-anthropic-billing-header: rotating\r\n\r\nreal\ud800"`, `"x-anthropic-billing-header: only"`, `"prefix\nx-anthropic-billing-header: retained"`, `[{"type":"text","text":"x-anthropic-billing-header: a\n\n"},{"type":"text","text":"second"},{"type":"text","text":""}]`} {
		body := base.Clone()
		body.Set("system", []byte(system))
		rows = append(rows, row{"request", body, requestRecord()})
	}
	for _, choice := range []string{`"any"`, `"auto"`, `"none"`, `"unknown"`, `null`, `{"type":"auto","disable_parallel_tool_use":true}`, `{"type":"any","disable_parallel_tool_use":false}`, `{"type":"tool","name":"Read"}`, `{"type":"tool","name":"BatchTool"}`, `{"type":"tool","name":"missing"}`} {
		for _, tools := range []string{`[]`, `[{"name":"BatchTool","input_schema":{}}]`, `[{"name":"Read","description":"read","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}},{"name":"BatchTool","input_schema":{}}]`} {
			for _, compat := range []string{`{}`, `{"requestCompatibility":{"parallelTools":"unsupported"}}`} {
				body := base.Clone()
				body.Set("tools", []byte(tools))
				body.Set("tool_choice", []byte(choice))
				rows = append(rows, row{"request", body, requestObject([]byte(compat))})
			}
		}
	}
	for _, thinking := range []string{`{"type":"enabled"}`, `{"type":"adaptive"}`, `{"type":"enabled","budget_tokens":null}`, `{"type":"enabled","budget_tokens":1024}`, `{"type":"enabled","budget_tokens":1025}`, `{"type":"enabled","budget_tokens":8192}`, `{"type":"enabled","budget_tokens":8193}`, `{"type":"enabled","budget_tokens":"bad"}`} {
		for _, effort := range []string{`null`, `"none"`, `"minimal"`, `"low"`, `"medium"`, `"high"`, `"xhigh"`, `"max"`, `"bad"`} {
			body := base.Clone()
			body.Set("thinking", []byte(thinking))
			body.Set("output_config", []byte(`{"effort":`+effort+`}`))
			rows = append(rows, row{"request", body, requestRecord()})
		}
	}
	for _, budget := range []string{`null`, `0`, `1`, `15`, `16`, `17`, `-1`, `1.5`, `9007199254740992`, `"16"`, `true`} {
		for _, options := range []string{`{"protocol":"openai_responses"}`, `{"protocol":"openai_responses","openAICodexOAuth":true}`, `{"protocol":"openai_responses","budgetSource":"default"}`, `{"protocol":"openai_responses","budgetSource":"default","requestCompatibility":{"maxOutputTokens":32,"outputTokenLimit":24}}`, `{"protocol":"openai_responses","requestCompatibility":{"outputTokenLimit":8}}`, `{"protocol":"openai_responses","requestCompatibility":{"outputTokenField":"omit"}}`} {
			body := base.Clone()
			body.Set("max_tokens", []byte(budget))
			rows = append(rows, row{"compatibility", body, requestObject([]byte(options))})
		}
	}
	for _, format := range []string{`null`, `false`, `[]`, `"bad"`, `{}`, `{"type":"json_schema","schema":[]}`, `{"type":"json_schema","name":"","description":"","schema":{"type":"object","properties":{"optional":{"type":"string"}}}}`, `{"type":"json_schema","name":"named","schema":{"type":"object"},"strict":true}`} {
		for _, unsupported := range []bool{false, true} {
			body := base.Clone()
			body.Set("output_format", []byte(format))
			opts := requestRecord()
			if unsupported {
				opts.Set("requestCompatibility", []byte(`{"structuredOutput":"unsupported"}`))
			}
			rows = append(rows, row{"request", body, opts})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-providerproxy-request.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PROVIDER_REQUEST_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(requestEncode(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source request %v %s", err, stderr.String())
	}
	var source []json.RawMessage
	if json.Unmarshal(output, &source) != nil || len(source) != len(rows) {
		t.Fatalf("Source rows %s", output)
	}
	for i, row := range rows {
		var value protocol.JSONObject
		var err error
		if row.Kind == "compatibility" {
			value, err = ResolveRequestCompatibility(row.Body, row.Options)
		} else {
			value, err = AnthropicToResponses(row.Body, row.Options)
		}
		result := requestRecord()
		if err != nil {
			result.Set("error", requestEncode(err.Error()))
			var compatibility *RequestCompatibilityError
			if errors.As(err, &compatibility) {
				result.Set("type", requestEncode(compatibility.Type()))
				result.Set("status", requestEncode(compatibility.Status()))
			}
		} else {
			result.Set("value", requestEncode(value))
		}
		got := requestEncode(result)
		want := protocol.StringifyValidatedJavaScriptJSON(source[i])
		if !bytes.Equal(got, want) {
			t.Errorf("row%d/%s actual%s want%s", i, row.Kind, got, want)
		}
	}
	t.Logf("compared %d actual Source request/compatibility outputs, including field order and UTF-16", len(rows))
}
