package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type searchRoute struct {
	origin    string
	transport http.RoundTripper
}

func (t searchRoute) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	u := *request.URL
	host := u.Hostname()
	u.Scheme = "http"
	u.Host = strings.TrimPrefix(t.origin, "http://")
	copy.URL = &u
	copy.Host = u.Host
	copy.Header = copy.Header.Clone()
	copy.Header.Set("X-Fixture-Original-Host", host)
	return t.transport.RoundTrip(copy)
}
func TestSelectionExternalRequestsAndNativeReducerMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Header.Get("Authorization")
		if r.Header.Get("X-Fixture-Original-Host") == "api.search.brave.com" {
			key = r.Header.Get("X-Subscription-Token")
		}
		calls = append(calls, r.Method+" "+r.URL.String()+" "+key+" "+string(body))
		query := r.URL.Query().Get("q")
		if r.Method == "POST" {
			var data protocol.JSONObject
			_ = json.Unmarshal(body, &data)
			query = data.String("query")
		}
		if query == "fail" {
			w.WriteHeader(502)
			fmt.Fprint(w, strings.Repeat("x", 600))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Fixture-Original-Host") == "api.tavily.com" {
			fmt.Fprint(w, `{"results":[{"title":"Go","url":"https://go.dev","extra":2},{"title":2,"url":"invalid"},{"title":"","url":""}]}`)
		} else {
			fmt.Fprint(w, `{"web":{"results":[{"title":"Search 😀","url":"https://example.org?a=~"},{"title":null,"url":"invalid"}]}}`)
		}
	}))
	defer server.Close()
	rows := []map[string]any{}
	for _, model := range []string{"deepseek-flash", "Claude-Sonnet", "x/claude:sonnet", "claudette", ""} {
		for _, mode := range []string{"auto", "anthropic", "tavily", "brave", "disabled", "invalid"} {
			rows = append(rows, map[string]any{"model": model, "settings": map[string]any{"webSearch": map[string]any{"mode": mode, "tavilyApiKey": " \ufeff fake-tavily ", "braveApiKey": " fake-brave "}}, "error": "server tool unsupported"})
		}
	}
	rows = append(rows, map[string]any{"model": "claude-fixture", "unsupported": " CLAUDE-fixture ", "settings": map[string]any{"webSearch": map[string]any{"mode": "anthropic"}}, "error": "4000"})
	external := []map[string]any{{"provider": "tavily", "input": map[string]any{"query": "Go", "allowed_domains": []string{"go.dev", ""}}}, {"provider": "brave", "input": map[string]any{"query": "😀 ~", "allowed_domains": []string{"a.dev", "b.dev", ""}}}, {"provider": "brave", "input": map[string]any{"query": "docs", "blocked_domains": []string{"blocked.dev"}}}, {"provider": "tavily", "input": map[string]any{"query": "fail"}}, {"provider": "brave", "input": map[string]any{"query": "fail"}}}
	reducers := []map[string]any{{"query": "Go", "blocks": []any{}}, {"query": "Go", "blocks": []any{map[string]any{"type": "text", "text": " before "}, map[string]any{"type": "server_tool_use", "id": "s1"}, map[string]any{"type": "web_search_tool_result", "tool_use_id": "s1", "content": []map[string]string{{"title": "Go", "url": "https://go.dev"}}}, map[string]any{"type": "text", "text": " after "}}}, {"query": "Go", "blocks": []any{map[string]any{"type": "web_search_tool_result", "tool_use_id": "s1", "content": map[string]string{"error_code": "max_uses_exceeded"}}, map[string]any{"type": "text", "text": "   "}}}, {"query": "😀", "blocks": []any{map[string]any{"type": "text", "text": "before"}, map[string]any{"type": "server_tool_use"}, map[string]any{"type": "server_tool_use"}, map[string]any{"type": "web_search_tool_result", "content": []any{}}, map[string]any{"type": "text", "text": "end"}}}}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-websearch.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WEBSEARCH_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "origin": server.URL, "rows": rows, "external": external, "reducers": reducers})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.2400s", e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Resolved                struct{ Provider string }
			Enabled, ShouldFallback bool
		}
		External []struct {
			Data  Output
			Error string
		}
		Reduced []struct {
			Data   Output
			Mapped protocol.JSONObject
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped%v %.2400s", source.Capabilities.Blocked, out)
	}
	sourceCalls := append([]string(nil), calls...)
	calls = nil
	fixed := time.UnixMilli(1770000000000)
	client := &http.Client{Transport: searchRoute{server.URL, http.DefaultTransport}}
	service, e := New(Options{Client: client, Settings: func() (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, Now: func() time.Time { return fixed }})
	if e != nil {
		t.Fatal(e)
	}
	for i, row := range rows {
		if unsupported, ok := row["unsupported"].(string); ok {
			service.unsupported[strings.ToLower(jsTrim(unsupported))] = true
		}
		var settings protocol.JSONObject
		_ = json.Unmarshal(raw(row["settings"]), &settings)
		provider := service.Resolve(row["model"].(string), ConfigFromSettings(settings))
		if provider != source.Rows[i].Resolved.Provider || ShouldFallback(row["error"].(string)) != source.Rows[i].ShouldFallback {
			t.Errorf("selection%d Go%s Source%+v", i, provider, source.Rows[i])
		}
	}
	for i, row := range external {
		var input protocol.JSONObject
		_ = json.Unmarshal(raw(row["input"]), &input)
		data, e := service.SearchExternal(ctx, row["provider"].(string), input, "fake-search-key")
		if e != nil {
			if e.Error() != source.External[i].Error {
				t.Errorf("external%d Go%v Source%s", i, e, source.External[i].Error)
			}
		} else if !reflect.DeepEqual(data, source.External[i].Data) {
			t.Errorf("external%d Go%s Source%s", i, raw(data), raw(source.External[i].Data))
		}
	}
	if !reflect.DeepEqual(sourceCalls, calls) {
		t.Errorf("physical requests Go%v Source%v", calls, sourceCalls)
	}
	for i, row := range reducers {
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(raw(row["blocks"]), &blocks)
		data := NativeOutput(blocks, raw(row["query"]), 0)
		if !reflect.DeepEqual(data, source.Reduced[i].Data) {
			t.Errorf("reducer%d Go%s Source%s", i, raw(data), raw(source.Reduced[i].Data))
		}
		var object protocol.JSONObject
		_ = json.Unmarshal(raw(data), &object)
		mapped, e := MapResult(object, quote("source-search-id"))
		var a, b any
		_ = json.Unmarshal(raw(mapped), &a)
		_ = json.Unmarshal(raw(source.Reduced[i].Mapped), &b)
		if e != nil || !reflect.DeepEqual(a, b) {
			t.Errorf("map%d Go%s %v Source%s", i, raw(mapped), e, raw(source.Reduced[i].Mapped))
		}
	}
}
