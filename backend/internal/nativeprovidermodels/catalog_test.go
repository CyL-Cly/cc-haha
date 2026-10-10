package nativeprovidermodels

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

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
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Source model catalog %v %s", err, stderr.String())
	}
	var observations []json.RawMessage
	if err = json.Unmarshal(output, &observations); err != nil {
		t.Fatalf("Source output %s %v", output, err)
	}
	return observations
}
func decode(v any) any {
	b, _ := json.Marshal(v)
	var result any
	_ = json.Unmarshal(b, &result)
	return result
}
func directOptions() Options {
	return Options{Environment: func() map[string]string { return map[string]string{} }, Settings: func(context.Context) (protocol.JSONObject, error) {
		return object(json.RawMessage(`{"network":{"proxy":{"mode":"direct"}}}`)), nil
	}}
}
func TestModelCandidatesAgainstActualSource(t *testing.T) {
	rows := []Input{{BaseURL: "https://api.example.com"}, {BaseURL: " https://api.example.com/// "}, {BaseURL: "https://api.example.com/v1"}, {BaseURL: "https://api.example.com/api/coding/paas/v4"}, {BaseURL: "https://api.deepseek.com/anthropic"}, {BaseURL: "https://api.example.com/api/anthropic"}, {BaseURL: "https://api.example.com/apps/anthropic"}, {BaseURL: "https://api.example.com/api/claudecode"}, {BaseURL: "https://api.example.com/api/coding"}, {BaseURL: "https://api.example.com/step_plan"}, {BaseURL: "https://api.example.com/claude"}, {BaseURL: "https://api.example.com/api"}, {BaseURL: "https://api.example.com/v1/chat/completions", IsFullURL: true}, {BaseURL: "https://api.example.com/openai/chat/completions", IsFullURL: true}, {BaseURL: "https://", IsFullURL: true}, {BaseURL: "completions", IsFullURL: true}, {BaseURL: " "}, {BaseURL: "///"}, {BaseURL: "file:///etc/hosts"}, {BaseURL: "https://api.example.com", ModelsURL: " file:///etc/hosts "}, {BaseURL: "https://api.example.com", ModelsURL: " ftp://example.com/list "}, {BaseURL: "https://api.example.com", ModelsURL: " https://private.example.com/list "}, {BaseURL: "https://api.example.com", ModelsURL: " "}}
	cases := []any{}
	got := []any{}
	for _, row := range rows {
		cases = append(cases, map[string]any{"action": "candidates", "input": map[string]any{"baseUrl": row.BaseURL, "isFullUrl": row.IsFullURL, "modelsUrl": row.ModelsURL}})
		candidates, err := BuildCandidates(row.BaseURL, row.IsFullURL, row.ModelsURL)
		if err != nil {
			got = append(got, map[string]any{"error": err.Error()})
		} else {
			got = append(got, map[string]any{"value": candidates})
		}
	}
	want := source(t, map[string]any{"cases": cases})
	for i := range got {
		if !reflect.DeepEqual(decode(got[i]), decode(want[i])) {
			t.Fatalf("candidate case %d\nGo %v\nSource %s", i, got[i], want[i])
		}
	}
}
func TestActualLoopbackCatalogFetchAgainstActualSource(t *testing.T) {
	key := "fake-provider-key-123456"
	type observation struct{ Path, Auth, Accept, XKey, Version string }
	var mu sync.Mutex
	calls := []observation{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, observation{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept"), r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Version")})
		mu.Unlock()
		path := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case path == "/compat/anthropic/v1/models":
			w.WriteHeader(404)
			w.Write([]byte("missing mount"))
		case path == "/compat/v1/models":
			w.WriteHeader(405)
		case path == "/compat/models":
			w.Write([]byte(`{"data":["flash"]}`))
		case strings.HasPrefix(path, "/miss/v4/"):
			w.WriteHeader(404)
			w.Write([]byte("not found"))
		case strings.HasPrefix(path, "/auth401/"):
			w.WriteHeader(401)
			w.Write([]byte("rejected " + key))
		case strings.HasPrefix(path, "/auth403/"):
			w.WriteHeader(403)
			w.Write([]byte("rejected " + key))
		case strings.HasPrefix(path, "/server500/"):
			w.WriteHeader(500)
			w.Write([]byte("bad " + key))
		case strings.HasPrefix(path, "/html/"):
			w.Write([]byte("<html>" + strings.Repeat("x", 508) + key + "</html>"))
		case strings.HasPrefix(path, "/authEnvelope/"):
			w.Write([]byte(`{"code":"403","msg":"身份验证失败。"}`))
		case strings.HasPrefix(path, "/nestedAuth/"):
			w.Write([]byte(`{"error":{"type":"invalid_api_key","message":"rejected ` + key + `"}}`))
		case strings.HasPrefix(path, "/unsupported/"):
			w.Write([]byte(`{"code":1000,"success":false,"message":"没有提供模型目录"}`))
		case strings.HasPrefix(path, "/localizedText/"):
			w.Write([]byte(`{"message":"Unauthorized is only text"}`))
		case strings.HasPrefix(path, "/unknown/"):
			w.Write([]byte(`{"unexpected":true}`))
		case strings.HasPrefix(path, "/empty/"):
			w.Write([]byte(`{"data":[],"models":["ignore"]}`))
		case strings.HasPrefix(path, "/items/"):
			w.Write([]byte(`{"items":[{"slug":"flash","provider":"vendor"},"flash"," fast ",17,null,[],{"id":3,"model":"fallback","owned_by":17,"owner":"owner"}]}`))
		case strings.HasPrefix(path, "/models/"):
			w.Write([]byte(`{"models":[{"name":"opus","vendor":"vendor"},"flash"]}`))
		case strings.HasPrefix(path, "/bare/"):
			w.Write([]byte("  \n[\"\\ue000\",\"😀\",{\"id\":\" flash \",\"owned_by\":\" owner \"},\"flash\",null,12] \n"))
		default:
			w.Write([]byte(`{"data":[{"id":"deepseek-flash","owned_by":"deepseek"},"deepseek-chat"]}`))
		}
	}))
	defer peer.Close()
	rows := []Input{{BaseURL: peer.URL + "/normal", APIKey: key}, {BaseURL: peer.URL + "/compat/anthropic", APIKey: key}, {BaseURL: peer.URL + "/miss/v4", APIKey: key}, {BaseURL: peer.URL + "/auth401", APIKey: key}, {BaseURL: peer.URL + "/auth403", APIKey: key}, {BaseURL: peer.URL + "/server500", APIKey: key}, {BaseURL: peer.URL + "/html", APIKey: key}, {BaseURL: peer.URL + "/authEnvelope", APIKey: key}, {BaseURL: peer.URL + "/nestedAuth", APIKey: key}, {BaseURL: peer.URL + "/unsupported", APIKey: key}, {BaseURL: peer.URL + "/localizedText", APIKey: key}, {BaseURL: peer.URL + "/unknown", APIKey: key}, {BaseURL: peer.URL + "/empty", APIKey: key}, {BaseURL: peer.URL + "/items", APIKey: key}, {BaseURL: peer.URL + "/models", APIKey: key}, {BaseURL: peer.URL + "/bare", APIKey: key}, {BaseURL: peer.URL + "/full/v1/chat/completions", APIKey: key, IsFullURL: true}, {BaseURL: peer.URL + "/full/chat/completions", APIKey: key, IsFullURL: true}, {BaseURL: peer.URL + "/normal", APIKey: " "}, {BaseURL: " ", APIKey: key}, {BaseURL: "file:///etc/hosts", APIKey: key}}
	service, _ := New(directOptions())
	got := []Result{}
	cases := []any{}
	for _, row := range rows {
		result, err := service.Fetch(context.Background(), row)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, result)
		cases = append(cases, map[string]any{"action": "fetch", "input": map[string]any{"baseUrl": row.BaseURL, "apiKey": row.APIKey, "isFullUrl": row.IsFullURL}})
	}
	mu.Lock()
	native := append([]observation(nil), calls...)
	calls = nil
	mu.Unlock()
	want := source(t, map[string]any{"cases": cases})
	for i := range got {
		if !reflect.DeepEqual(decode(got[i]), decode(want[i])) {
			g, _ := json.Marshal(got[i])
			t.Fatalf("catalog case %d\nGo %s\nSource %s", i, g, want[i])
		}
	}
	mu.Lock()
	original := append([]observation(nil), calls...)
	mu.Unlock()
	if !reflect.DeepEqual(native, original) {
		t.Fatalf("transport differs\nGo %+v\nSource %+v", native, original)
	}
	for _, call := range native {
		if call.Auth != "Bearer "+key || call.Accept != "application/json" || call.XKey != "" || call.Version != "" {
			t.Fatal("catalog credential protocol", call)
		}
	}
}
func TestCatalogTimeoutAndProxySettingsAgainstActualSource(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stalled/v1/models" {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
			return
		}
		w.Write([]byte(`{"data":["flash"]}`))
	}))
	defer peer.Close()
	key := "fake-private-credential"
	type row struct {
		input    Input
		settings string
		env      map[string]string
	}
	rows := []row{{Input{BaseURL: peer.URL + "/stalled", APIKey: key}, `{"network":{"proxy":{"mode":"direct"}}}`, map[string]string{}}, {Input{BaseURL: peer.URL, APIKey: key}, `{"network":{"proxy":{"mode":"system"}}}`, map[string]string{"CC_HAHA_SYSTEM_PROXY_ERROR": "private proxy resolution failure"}}, {Input{BaseURL: peer.URL, APIKey: key}, `{"network":{"proxy":{"mode":"direct"}}}`, map[string]string{"HTTPS_PROXY": "http://127.0.0.1:1"}}, {Input{BaseURL: peer.URL, APIKey: key}, `{"network":{"proxy":{"mode":"manual","url":"http://127.0.0.1:1"}}}`, map[string]string{}}}
	cases := []any{}
	got := []Result{}
	for _, row := range rows {
		opts := directOptions()
		opts.Timeout = 50 * time.Millisecond
		opts.Environment = func() map[string]string { return row.env }
		opts.Settings = func(context.Context) (protocol.JSONObject, error) { return object(json.RawMessage(row.settings)), nil }
		s, _ := New(opts)
		result, err := s.Fetch(context.Background(), row.input)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, result)
		cases = append(cases, map[string]any{"action": "fetch", "input": map[string]any{"baseUrl": row.input.BaseURL, "apiKey": row.input.APIKey}, "settings": json.RawMessage(row.settings), "env": row.env})
	}
	want := source(t, map[string]any{"timeoutMs": 50, "cases": cases})
	for i := range got {
		if !reflect.DeepEqual(decode(got[i]), decode(want[i])) {
			g, _ := json.Marshal(got[i])
			t.Fatalf("proxy/timeout case %d\nGo %s\nSource %s", i, g, want[i])
		}
	}
}
