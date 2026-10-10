package clibootstrap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestNativeMCPOrdinaryWebActualSourceAndGoHTTPSModelConsumers(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"skipWebFetchPreflight":true}`), 0600)
	var mu sync.Mutex
	requests := []map[string]any{}
	rawRequests := []string{}
	gets := 0
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			mu.Lock()
			gets++
			mu.Unlock()
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html><body><h1>Local source article</h1><p>actual physical loopback content</p></body></html>")
			return
		}
		var body map[string]any
		rawBody, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(rawBody, &body); err != nil {
			t.Error(err)
			http.Error(w, "bad input", 400)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		rawRequests = append(rawRequests, string(rawBody))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		model := body["model"]
		events := []any{map[string]any{"type": "message_start", "message": map[string]any{"id": "mcp-loopback", "type": "message", "role": "assistant", "model": model, "content": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}}, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "LOCAL_ACTUAL_MODEL_RESPONSE"}}, map[string]any{"type": "content_block_stop", "index": 0}, map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 1}}, map[string]any{"type": "message_stop"}}
		for _, event := range events {
			value := event.(map[string]any)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", value["type"], rawMain(value))
		}
	}))
	defer peer.Close()
	certificate := filepath.Join(home, "private-cert.pem")
	_ = os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "CLAUDE_CODE_TMPDIR": home, "PATH": "/usr/bin:/bin", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "USER_TYPE": "external", "NODE_ENV": "production", "ANTHROPIC_API_KEY": "fixture-not-a-secret", "ANTHROPIC_BASE_URL": peer.URL, "NODE_EXTRA_CA_CERTS": certificate, "CLAUDE_CODE_DISABLE_FAST_MODE": "1"}
	fixture, _ := filepath.Abs("../../testdata/native-mcp-ordinary.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	source := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	source.Dir = home
	for key, value := range env {
		source.Env = append(source.Env, key+"="+value)
	}
	source.Env = append(source.Env, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(home), "CC_HAHA_MCP_ORDINARY_SOURCE_FIXTURE=1")
	input, _ := source.StdinPipe()
	output, _ := source.StdoutPipe()
	var stderr bytes.Buffer
	source.Stderr = &stderr
	if err := source.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = source.Process.Kill() }()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	type call struct {
		Name   string
		Args   map[string]any
		Result protocol.JSONObject
	}
	cases := []call{{Name: "WebSearch", Args: map[string]any{"query": "actual local MCP transport"}}, {Name: "WebFetch", Args: map[string]any{"url": peer.URL + "/article", "prompt": "Summarize this actual content"}}}
	for i := range cases {
		packet := map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "tools/call", "params": map[string]any{"name": cases[i].Name, "arguments": cases[i].Args}}
		_, _ = input.Write(append(rawMain(packet), '\n'))
		if !scanner.Scan() {
			t.Fatal("Source model tool response absent", scanner.Err(), stderr.String())
		}
		var response protocol.JSONObject
		_ = response.UnmarshalJSON(scanner.Bytes())
		cases[i].Result, _ = response.Object("result")
		if string(cases[i].Result.Get("isError")) == "true" {
			t.Fatal("Source model tool failed", string(scanner.Bytes()), stderr.String())
		}
	}
	_ = input.Close()
	if err := source.Wait(); err != nil {
		t.Fatal(err, stderr.String())
	}
	mu.Lock()
	if len(requests) != 2 || gets != 1 {
		t.Fatalf("Source physical requests models=%d gets=%d", len(requests), gets)
	}
	sourceRequests := append([]map[string]any{}, requests...)
	mu.Unlock()
	n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		// Source reads its empty external credential store here. This fake
		// primitive resolves absence and never invokes the actual keychain.
		return nil, os.ErrNotExist
	}
	command, err := NewMainCommand(MainCommandOptions{Arguments: []string{"mcp", "serve"}, Native: n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), NonInteractive: true, Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer command.Close()
	host, err := NewNativeMCPToolContext(command, "darwin", false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	for _, item := range cases {
		got, err := host.Call(ctx, item.Name, rawMain(item.Args))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(mcpOrdinaryResult(t, got), mcpOrdinaryResult(t, item.Result)) {
			t.Errorf("%s model result differs Go=%v Source=%v", item.Name, mcpOrdinaryResult(t, got), mcpOrdinaryResult(t, item.Result))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 4 || gets != 2 {
		t.Fatalf("Go physical model requests=%d gets=%d", len(requests)-2, gets-1)
	}
	cch := regexp.MustCompile(`cch=[0-9a-f]{5};`)
	unsigned := []string{}
	for _, body := range rawRequests {
		unsigned = append(unsigned, cch.ReplaceAllString(body, "cch=00000;"))
	}
	signatureFixture, _ := filepath.Abs("../../testdata/native-mcp-cch.fixture.ts")
	signature := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", signatureFixture)
	signature.Dir = home
	signature.Env = source.Env
	signature.Env = append(signature.Env, "CC_HAHA_MCP_CCH_FIXTURE=1")
	signature.Stdin = bytes.NewReader(rawMain(unsigned))
	signed, err := signature.Output()
	if err != nil {
		t.Fatal("actual Source CCH verification", err)
	}
	var sourceSigned []string
	if err := json.Unmarshal(signed, &sourceSigned); err != nil {
		t.Fatal(err)
	}
	if len(sourceSigned) != 4 {
		t.Fatal("Source CCH results missing")
	}
	for i, body := range rawRequests {
		if sourceSigned[i] != body || provider.SignClaudeCodeCCHInString(unsigned[i]) != body {
			t.Errorf("request%d actual producer CCH differs from original Source", i)
		}
	}
	for _, body := range requests {
		if system, ok := body["system"].([]any); ok {
			for _, block := range system {
				if text, ok := block.(map[string]any); ok {
					if value, ok := text["text"].(string); ok {
						text["text"] = cch.ReplaceAllString(value, "cch=00000;")
					}
				}
			}
		}
	}
	for i, want := range sourceRequests {
		got := requests[i+2]
		for _, key := range []string{"model", "messages", "system", "tools", "thinking", "stream", "max_tokens", "temperature", "output_config"} {
			if !reflect.DeepEqual(got[key], want[key]) {
				t.Errorf("request%d %s differs Go=%v Source=%v", i, key, got[key], want[key])
			}
		}
	}
}
