package providerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestOpenAIIdentityModelsPolicyTurnAndFetchRequestAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []protocol.JSONObject{}
	modelIDs := []string{"", "unknown", "claude-haiku-4-5", "claude-sonnet-5-5", "claude-opus-5-5", "haiku-sonnet-opus", "gpt-6", "gpt-5.6", "gpt-5.4-pro", "gpt-5.5-pro", "gpt-5.4-nano", "gpt-5.3-codex-spark", "gpt-5.2", "gpt-5.2-codex", "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini", "gpt-5-codex", "gpt-5", "gpt-5-mini", "gpt-5-nano", "o1", "o9-private", "oX", "x/gpt-6-sol[1m]:1m", "\ufeff GPT-6-SOL\u00a0", "x/o1[1m]", "gpt-6-sol:1m[1m]"}
	for _, model := range OpenAIModelCatalog() {
		modelIDs = append(modelIDs, model.Value)
	}
	for _, model := range modelIDs {
		for _, efforts := range []string{`[]`, `[null,false,0,"bad"]`, `["max","high"]`, `["none","minimal","low"]`, `["HIGH","xhigh","medium"]`, `["medium","low"]`} {
			rows = append(rows, requestRecord(requestField("kind", "models"), requestField("model", model), requestField("efforts", json.RawMessage(efforts))))
		}
		for _, env := range []string{`{"OPENAI_CODEX_MODEL":" \ufeffexplicit-model\u00a0 "}`, `{"OPENAI_CODEX_MODEL":" \ufeff ","OPENAI_CODEX_HAIKU_MODEL":"private-haiku","OPENAI_CODEX_SONNET_MODEL":"private-sonnet","OPENAI_CODEX_OPUS_MODEL":" private-opus "}`} {
			rows = append(rows, requestRecord(requestField("kind", "models"), requestField("model", model), requestField("efforts", []string{}), requestField("env", requestObject([]byte(env)))))
		}
	}
	for _, session := range []string{`null`, `""`, `"  "`, `"old-root"`, `" root\ud800\udfff "`, `"5ABC9876-12AB-48CD-9EFF-123456789ABC"`, `"\ufeff5abc9876-12ab-48cd-9eff-123456789abc\u2029"`} {
		for _, agent := range []string{`null`, `""`, `"child"`, `" child "`, `"\ud800-agent"`} {
			for _, body := range []string{`{}`, `{"metadata":{"user_id":"account_session_from-meta"}}`, `{"metadata":{"user_id":"_session_","session_id":" fallback "}}`, `{"metadata":{"user_id":"_session_\ud800x_session_y","session_id":"fallback"}}`, `{"metadata":{"session_id":" "}}`, `{"metadata":{"user_id":false,"session_id":0}}`} {
				rows = append(rows, requestRecord(requestField("kind", "identity"), requestField("session", json.RawMessage(session)), requestField("agent", json.RawMessage(agent)), requestField("body", requestObject([]byte(body)))))
			}
		}
	}
	policies := []string{`null`, `false`, `"policy text"`, `[]`, `{}`, `{"code":"content_policy"}`, `{"code":"content_policy","message":" \ufeff "}`, `{"code":"content_policy","message":"raw\ud800 message"}`, `{"code":"cyber_policy","message":false}`, `{"error":{"code":"content_policy_violation","message":"Original"},"response":{"code":"cyber_policy"}}`, `{"message":"403 {\"error\":{\"code\":\"content_policy\",\"message\":\"from SDK\"}}"}`, `{"message":"\ufeff{\"code\":\"cyber_policy\"}"}`, `{"message":"403 content policy rejected"}`, `{"message":"{invalid"}`}
	for _, key := range []string{"error", "response", "originalError", "cause", "unrelated"} {
		value := `{"code":"cyber_policy"}`
		for depth := 1; depth <= 14; depth++ {
			value = `{"` + key + `":` + value + `}`
			policies = append(policies, value)
		}
	}
	for _, input := range policies {
		rows = append(rows, requestRecord(requestField("kind", "policy"), requestField("input", json.RawMessage(input))))
	}
	for _, marker := range []string{`null`, `""`, `"1"`, `"0"`, `" 1 "`} {
		for _, override := range []string{"", "0", "-1", "1", "+12", "  \ufeff6500.5ms", "1e3", "0x10", "bad", "Infinity", strings.Repeat("9", 400)} {
			for _, fallback := range []int{1000, 450000} {
				rows = append(rows, requestRecord(requestField("kind", "streamPolicy"), requestField("marker", json.RawMessage(marker)), requestField("override", override), requestField("fallback", fallback), requestField("crossed", fallback == 450000)))
			}
		}
	}
	for _, aborted := range []bool{false, true} {
		for _, actions := range []string{`[]`, `[{"kind":"capture","value":null},{"kind":"capture","value":""},{"kind":"capture","value":"first"},{"kind":"capture","value":"second"}]`, `[{"kind":"capture","value":"first"},{"kind":"abort"},{"kind":"capture","value":"second"}]`, `[{"kind":"capture","value":" "},{"kind":"dispose"},{"kind":"dispose"},{"kind":"capture","value":"second"}]`} {
			rows = append(rows, requestRecord(requestField("kind", "turnState"), requestField("aborted", aborted), requestField("actions", json.RawMessage(actions))))
		}
	}
	base := requestObject([]byte(`{"model":"claude-sonnet-5-5","system":"x-anthropic-billing-header: rotating\n\nreal","messages":[{"role":"user","content":"hello"}],"max_tokens":16,"stream":true,"thinking":{"type":"enabled","budget_tokens":1024},"tools":[{"name":"Read","input_schema":{"type":"object"}}]}`))
	tokens := requestObject([]byte(`{"accessToken":"private-fixture-access","refreshToken":"private-fixture-refresh","expiresAt":1000000000000000,"accountId":"private-account"}`))
	for _, model := range []string{"claude-haiku-4-5", "claude-sonnet-5-5", "claude-opus-5-5", "gpt-6-sol", "gpt-5.4", "gpt-6-astra"} {
		for _, effort := range []string{`null`, `"minimal"`, `"low"`, `"max"`} {
			for _, stream := range []bool{true, false} {
				body := base.Clone()
				body.Set("model", requestEncode(model))
				body.Set("output_config", []byte(`{"effort":`+effort+`}`))
				body.Set("stream", requestEncode(stream))
				body.Set("metadata", []byte(`{"user_id":"account_session_stable-meta-cache"}`))
				rows = append(rows, requestRecord(requestField("kind", "request"), requestField("body", body), requestField("tokens", tokens), requestField("session", "5abc9876-12ab-48cd-9eff-123456789abc"), requestField("agent", "child-fixture"), requestField("turnState", "first-routing-state"), requestField("env", map[string]string{"CC_HAHA_OPENAI_REASONING_EFFORT": "high"})))
			}
		}
	}
	for _, tokenJSON := range []string{`null`, `{"accessToken":"","refreshToken":"","expiresAt":1000000000000000}`, `{"accessToken":"private-fixture-access","refreshToken":"private-fixture-refresh","expiresAt":1000000000000000,"accountId":""}`} {
		for _, session := range []string{"", "root-fixture"} {
			rows = append(rows, requestRecord(requestField("kind", "request"), requestField("body", base), requestField("tokens", json.RawMessage(tokenJSON)), requestField("session", session)))
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-providerproxy-openai.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_REQUEST_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(requestEncode(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source OpenAI %v %s", err, stderr.String())
	}
	source := requestObject(output)
	if want := protocol.StringifyValidatedJavaScriptJSON(source.Get("catalog")); !bytes.Equal(requestEncode(OpenAIModelCatalog()), want) {
		t.Errorf("bundled catalog differs actual%s want%s", requestEncode(OpenAIModelCatalog()), want)
	}
	wanted := requestArray(source.Get("rows"))
	if len(wanted) != len(rows) {
		t.Fatalf("Source row count %d expected%d", len(wanted), len(rows))
	}
	for i, row := range rows {
		got := openAISourceRow(row)
		want := protocol.StringifyValidatedJavaScriptJSON(wanted[i])
		if !bytes.Equal(requestEncode(got), want) {
			t.Errorf("row%d/%s actual%s want%s", i, row.String("kind"), requestEncode(got), want)
		}
	}
	t.Logf("compared catalog and %d actual Source model/identity/cache/policy/turn/timeout/fetch request profiles", len(rows))
}

func openAISourceRow(row protocol.JSONObject) any {
	env := map[string]string{}
	for _, field := range requestObject(row.Get("env")).OwnProperties() {
		var key, value string
		_ = json.Unmarshal(field.NameJSON, &key)
		_ = json.Unmarshal(field.Value, &value)
		env[key] = value
	}
	switch row.String("kind") {
	case "models":
		model := row.String("model")
		result := requestRecord(requestField("model", ResolveOpenAICodexModel(model, env)), requestField("responses", IsOpenAIResponsesModel(model)))
		if display := OpenAIModelDisplayName(model); display != "" {
			result.Set("display", requestEncode(display))
		} else {
			result.Set("display", []byte("null"))
		}
		if window := OpenAICodexContextWindow(model); window != 0 {
			result.Set("context", requestEncode(window))
		} else {
			result.Set("context", []byte("null"))
		}
		result.Set("effort", requestEncode(ResolveOpenAIReasoningEffort(model, requestArray(row.Get("efforts")))))
		result.Set("entry", requestEncode(OpenAIModelCatalogEntry(model, OpenAIModelCatalog())))
		return result
	case "identity":
		result := requestRecord(requestField("identity", nil), requestField("cacheKey", nil))
		if identity, ok := ResolveOpenAIRequestIdentity(row.Get("session"), row.Get("agent")); ok {
			result.Set("identity", requestEncode(identity))
		}
		if key := ResolvePromptCacheKey(requestObject(row.Get("body")), row.Get("session")); key != nil {
			result.Set("cacheKey", key)
		}
		return result
	case "policy":
		if policy, ok := GetOpenAIPolicyError(row.Get("input")); ok {
			return policy
		}
		return nil
	case "streamPolicy":
		marker := strings.Trim(row.String("marker"), "\t\r\n ")
		return requestRecord(requestField("timeout", ResolveOpenAICodexFirstTokenTimeout(marker, requestNumber(row.Get("fallback")), row.String("override"))), requestField("retry", CanRetryOpenAICodexBufferedStream(marker, string(row.Get("crossed")) == "true")))
	case "turnState":
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if string(row.Get("aborted")) == "true" {
			cancel()
		}
		state := NewOpenAICodexTurnState(ctx)
		observations := []json.RawMessage{}
		for _, actionRaw := range requestArray(row.Get("actions")) {
			action := requestObject(actionRaw)
			switch action.String("kind") {
			case "capture":
				state.Capture(action.String("value"))
			case "abort":
				cancel()
			case "dispose":
				state.Dispose()
			}
			value := requestEncode(nil)
			if captured := state.Get(); captured != "" {
				value = requestEncode(captured)
			}
			observations = append(observations, value)
		}
		state.Dispose()
		return observations
	case "request":
		prepared, err := PrepareOpenAIRequest(requestObject(row.Get("body")), requestObject(row.Get("tokens")), env, row.Get("session"), row.Get("agent"), row.String("turnState"))
		if err != nil {
			return requestRecord(requestField("error", err.Error()))
		}
		keys := []string{}
		for _, field := range prepared.Headers.OwnProperties() {
			var key string
			_ = json.Unmarshal(field.NameJSON, &key)
			keys = append(keys, key)
		}
		sort.Strings(keys)
		headers := requestRecord()
		for _, key := range keys {
			headers.Set(key, prepared.Headers.Get(key))
		}
		return requestRecord(requestField("request", requestRecord(requestField("endpoint", prepared.Endpoint), requestField("body", prepared.Body), requestField("headers", headers))))
	}
	return nil
}
