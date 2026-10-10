package nativeprovidertest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var frozen = time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)

func source(t *testing.T, input any) []json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-provider-model-catalog.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = home
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_PROVIDER_MODELS_FIXTURE=1"}
	b, _ := json.Marshal(input)
	command.Stdin = bytes.NewReader(b)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("Source provider test %v %s", err, stderr.String())
	}
	var result []json.RawMessage
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatalf("Source output %s %v", out, err)
	}
	return result
}
func options() Options {
	return Options{Environment: func() map[string]string { return map[string]string{} }, Settings: func(context.Context) (protocol.JSONObject, error) {
		return object([]byte(`{"network":{"proxy":{"mode":"direct"}}}`)), nil
	}, Version: "fixture-version", Now: func() time.Time { return frozen }}
}
func decode(value any) any {
	b, _ := json.Marshal(value)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}
func wireInput(input Input) map[string]any {
	v := map[string]any{"baseUrl": input.BaseURL, "apiKey": input.APIKey, "modelId": input.ModelID, "apiFormat": input.APIFormat}
	if input.AuthStrategy != "" {
		v["authStrategy"] = input.AuthStrategy
	}
	if input.PresetID != "" {
		v["presetId"] = input.PresetID
	}
	if input.SupportsNestedMedia != nil {
		v["supportsNestedToolResultMedia"] = *input.SupportsNestedMedia
	}
	if input.RequestCompatibility.IsObject() {
		v["requestCompatibility"] = input.RequestCompatibility
	}
	return v
}
func TestProviderDirectAndActualProxyPipelineAgainstSource(t *testing.T) {
	type call struct {
		Path, Auth, XKey, Version, Session, Agent string
		Body                                      any
	}
	var mu sync.Mutex
	calls := []call{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		agent := r.Header.Get("User-Agent")
		if !strings.HasPrefix(agent, "cc-haha/") {
			agent = ""
		}
		sid := r.Header.Get("X-Opencode-Session")
		if sid != "" {
			if !strings.HasPrefix(sid, "cc-haha-probe-") {
				t.Error("invalid probe session")
			}
			sid = "<probe>"
		}
		mu.Lock()
		calls = append(calls, call{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Version"), sid, agent, body})
		mu.Unlock()
		model, _ := body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/reject/") {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"message":"invalid fake key"}}`))
			return
		}
		if strings.Contains(r.URL.Path, "/empty/") {
			w.Write([]byte("null"))
			return
		}
		if strings.Contains(r.URL.Path, "/invalid/") {
			w.Write([]byte(`{"unexpected":true}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			if strings.Contains(r.URL.Path, "/bad-pipeline/") {
				w.Write([]byte(`{"choices":[{}]}`))
			} else {
				w.Write([]byte(`{"id":"chat-1","model":"` + model + `","choices":[{"finish_reason":"stop","message":{"content":"ok","role":"assistant"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
			}
		} else if strings.HasSuffix(r.URL.Path, "/responses") {
			w.Write([]byte(`{"id":"resp-1","model":"` + model + `","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
		} else {
			w.Write([]byte(`{"id":"msg-1","type":"message","model":"` + model + `","content":[{"type":"text","text":"ok"}]}`))
		}
	}))
	defer peer.Close()
	noMedia := false
	rows := []Input{}
	for _, auth := range []string{"api_key", "auth_token", "auth_token_empty_api_key", "dual_same_token", "dual_dummy"} {
		rows = append(rows, Input{BaseURL: peer.URL + "/normal/v1", APIKey: "fake-key-123456", ModelID: "deepseek-flash[1m]", APIFormat: "anthropic", AuthStrategy: auth})
	}
	rows = append(rows, Input{BaseURL: peer.URL + "/media/v1", APIKey: "fake-key-123456", ModelID: "flash", APIFormat: "anthropic", SupportsNestedMedia: &noMedia}, Input{BaseURL: peer.URL + "/chat", APIKey: "fake-key-123456", ModelID: "gpt-5.3", APIFormat: "openai_chat"}, Input{BaseURL: peer.URL + "/responses/v1", APIKey: "fake-key-123456", ModelID: "gpt-5", APIFormat: "openai_responses"}, Input{BaseURL: peer.URL + "/bad-pipeline", APIKey: "fake-key-123456", ModelID: "flash", APIFormat: "openai_chat"}, Input{BaseURL: peer.URL + "/reject", APIKey: "fake-key-123456", ModelID: "flash", APIFormat: "openai_chat"}, Input{BaseURL: peer.URL + "/empty", APIKey: "fake-key-123456", ModelID: "flash", APIFormat: "anthropic"}, Input{BaseURL: peer.URL + "/invalid", APIKey: "fake-key-123456", ModelID: "flash", APIFormat: "anthropic"}, Input{BaseURL: peer.URL + "/opencode/v1", APIKey: "fake-key-123456", ModelID: "claude-sonnet-4", APIFormat: "anthropic", PresetID: "opencode-go"}, Input{BaseURL: peer.URL + "/opencode/v1", APIKey: "fake-key-123456", ModelID: "gpt-5", APIFormat: "anthropic", PresetID: "opencode-go"}, Input{BaseURL: peer.URL + "/compat", APIKey: "fake-key-123456", ModelID: "flash", APIFormat: "openai_chat", RequestCompatibility: object([]byte(`{"outputTokenLimit":5,"outputTokenField":"max_completion_tokens"}`))}, Input{BaseURL: peer.URL + "/omit", APIKey: "fake-key-123456", ModelID: "flash", APIFormat: "openai_chat", RequestCompatibility: object([]byte(`{"outputTokenField":"omit"}`))}, Input{BaseURL: peer.URL + "/limit", APIKey: "fake-key-123456", ModelID: "flash", APIFormat: "openai_responses", RequestCompatibility: object([]byte(`{"outputTokenLimit":5}`))})
	s, _ := New(options())
	got := []Result{}
	cases := []any{}
	for _, row := range rows {
		result, err := s.Test(context.Background(), row)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, result)
		cases = append(cases, map[string]any{"action": "connectionTest", "input": wireInput(row)})
	}
	mu.Lock()
	native := append([]call(nil), calls...)
	calls = nil
	mu.Unlock()
	want := source(t, map[string]any{"now": frozen.Format(time.RFC3339Nano), "cases": cases})
	for i := range got {
		if !reflect.DeepEqual(decode(got[i]), decode(want[i])) {
			g, _ := json.Marshal(got[i])
			t.Fatalf("probe case %d\nGo %s\nSource %s", i, g, want[i])
		}
	}
	mu.Lock()
	original := append([]call(nil), calls...)
	mu.Unlock()
	if !reflect.DeepEqual(native, original) {
		g, _ := json.MarshalIndent(native, "", " ")
		w, _ := json.MarshalIndent(original, "", " ")
		t.Fatalf("real proxy wire differs\nGo %s\nSource %s", g, w)
	}
}
func TestProviderResponseTransformsAgainstActualSource(t *testing.T) {
	type row struct{ format, raw string }
	rows := []row{{"openai_chat", `{"id":"chat","model":"flash","choices":[{"finish_reason":"stop","message":{"content":"ok","reasoning_content":"thinking"}}],"usage":{"prompt_tokens":20,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":5}}}`}, {"openai_chat", `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"tool-1","function":{"name":"read","arguments":"{bad"}}]}}]}`}, {"openai_chat", `{"choices":[{"finish_reason":"length","message":{"tool_calls":[{"id":"","function":{"name":"","arguments":"{bad"}}]}}]}`}, {"openai_chat", `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[]}}]}`}, {"openai_chat", `{"choices":[{"finish_reason":"stop","message":{"tool_calls":[{"id":"x","function":{"name":"read","arguments":"[]"}}]}}]}`}, {"openai_chat", `{"choices":[{"finish_reason":null,"message":{"content":"ok"}}]}`}, {"openai_responses", `{"id":"resp","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"},{"type":"refusal","refusal":"denied"}]},{"type":"reasoning","summary":[{"text":"thinking"}]}],"usage":{"total_tokens":8,"output_tokens":2}}`}, {"openai_responses", `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"function_call","name":"read","call_id":"id","arguments":"[]"}]}`}, {"openai_responses", `{"status":"completed","output":[{"type":"function_call","name":"read","call_id":"id","arguments":"{bad"}]}`}, {"openai_responses", `{"status":"failed","error":{"message":"remote failure"}}`}, {"openai_responses", `{"status":"incomplete","incomplete_details":{"reason":"unsupported"},"output":[]}`}, {"openai_responses", `{"output":[]}`}, {"openai_responses", `{"status":"failed","error":{"code":"cyber_policy"}}`}}
	cases := []any{}
	got := []any{}
	for _, r := range rows {
		value, err := TransformResponse([]byte(r.raw), "fallback", r.format, frozen)
		if err != nil {
			got = append(got, map[string]any{"error": err.Error()})
		} else {
			got = append(got, map[string]any{"value": value})
		}
		cases = append(cases, map[string]any{"action": "transformResponse", "format": r.format, "model": "fallback", "value": json.RawMessage(r.raw)})
	}
	want := source(t, map[string]any{"now": frozen.Format(time.RFC3339Nano), "cases": cases})
	for i := range got {
		if !reflect.DeepEqual(decode(got[i]), decode(want[i])) {
			g, _ := json.Marshal(got[i])
			t.Fatalf("transform case %d\nGo %s\nSource %s", i, g, want[i])
		}
	}
}
func TestProviderInputSchemaAgainstActualSource(t *testing.T) {
	rows := []string{`null`, `[]`, `{}`, `{"baseUrl":"not-url","apiKey":"","modelId":""}`, `{"baseUrl":"http://localhost","apiKey":"fake","modelId":"flash"}`, `{"baseUrl":"http://localhost","apiKey":"fake","modelId":"flash","authStrategy":"bad","apiFormat":"bad"}`, `{"baseUrl":"http://localhost","apiKey":"fake","modelId":"flash","supportsNestedToolResultMedia":null}`, `{"baseUrl":"http://localhost","apiKey":"fake","modelId":"flash","requestCompatibility":{"maxOutputTokens":0,"outputTokenLimit":3.5,"outputTokenField":"bad","sampling":"bad"}}`}
	cases := []any{}
	got := [][]string{}
	for _, row := range rows {
		_, issues := ParseInput([]byte(row))
		got = append(got, issues)
		cases = append(cases, map[string]any{"action": "testSchema", "value": json.RawMessage(row)})
	}
	want := source(t, map[string]any{"cases": cases})
	for i := range got {
		if !reflect.DeepEqual(decode(got[i]), decode(want[i])) {
			t.Fatalf("schema case %d Go %v Source %s", i, got[i], want[i])
		}
	}
}
func TestSavedProviderMissingCredentialNeverFetches(t *testing.T) {
	opts := options()
	opts.Provider = func(context.Context, string) (protocol.JSONObject, error) {
		return object([]byte(`{"baseUrl":"http://127.0.0.1:1","apiKey":"","presetId":"custom","models":{"main":"flash"}}`)), nil
	}
	s, _ := New(opts)
	result, err := s.Saved(context.Background(), "saved", "")
	if err != nil || result.Connectivity.Success || result.Connectivity.Error != "Missing baseUrl or apiKey" || result.Proxy != nil {
		t.Fatal(result, err)
	}
	encodedResult, _ := json.Marshal(result)
	if regexp.MustCompile(`modelUsed|httpStatus`).Match(encodedResult) {
		t.Fatal("invented request", string(encodedResult))
	}
}
