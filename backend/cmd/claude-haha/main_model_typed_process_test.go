package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublicCompiledCLIActualTypedModelPTYValidationNextASKAndClose(t *testing.T) {
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("native Unix PTY requires Python")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home, work, tmp := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "tmp")
	config := filepath.Join(home, ".claude")
	for _, path := range []string{home, work, tmp, config} {
		if e = os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	var mu sync.Mutex
	var bodies []map[string]any
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if body["stream"] != true {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"actual-public-validation","content":[],"usage":{}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, entry := range [][2]string{{"message_start", `{"type":"message_start","message":{"id":"actual-public-ask","type":"message","role":"assistant","model":"MiXeD-Public-Custom","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`}, {"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`}, {"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"PUBLIC_TYPED_NEXT"}}`}, {"content_block_stop", `{"type":"content_block_stop","index":0}`}, {"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`}, {"message_stop", `{"type":"message_stop"}`}} {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", entry[0], entry[1])
		}
	}))
	defer peer.Close()
	ca := filepath.Join(root, "root.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	settings := []byte(`{"model":"sonnet","effortLevel":"low","future":{"keep":true}}`)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), settings, 0600)
	global, _ := json.Marshal(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{work: map[string]any{"hasTrustDialogAccepted": true}}, "future": true})
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), global, 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	binary := filepath.Join(root, "go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	driver := exec.CommandContext(ctx, python, "testdata/model_typed_pty.py", binary, root, work)
	driver.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "PATH=/usr/bin:/bin", "TERM=xterm-256color", "NODE_ENV=test", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "ANTHROPIC_API_KEY=owned-public-model-key", "ANTHROPIC_BASE_URL=" + peer.URL, "NODE_EXTRA_CA_CERTS=" + ca, "CLAUDE_CODE_MAX_RETRIES=0"}
	out, e := driver.CombinedOutput()
	if e != nil {
		t.Fatal("actual public TUI", e, string(out))
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal(string(out))
	}
	saved, e := os.ReadFile(filepath.Join(config, "settings.json"))
	if e != nil {
		t.Fatal(e)
	}
	var actual map[string]any
	_ = json.Unmarshal(saved, &actual)
	if actual["model"] != nil || actual["effortLevel"] != "low" || actual["future"] == nil {
		t.Fatal("actual typed default deletion/unknown fields", string(saved))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || bodies[0]["model"] != "MiXeD-Public-Custom" || bodies[0]["max_tokens"] != float64(1) || bodies[0]["stream"] != nil || bodies[1]["model"] != "MiXeD-Public-Custom" || bodies[1]["stream"] != true {
		t.Fatal("actual public custom validation and next same Harness", bodies)
	}
	visibleOutput, helpVisible := false, false
	for _, message := range bodies[1]["messages"].([]any) {
		value := message.(map[string]any)["content"]
		texts := []string{}
		if text, ok := value.(string); ok {
			texts = append(texts, text)
		}
		if blocks, ok := value.([]any); ok {
			for _, block := range blocks {
				if text, ok := block.(map[string]any)["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
		for _, text := range texts {
			visibleOutput = visibleOutput || strings.HasPrefix(text, "<local-command-stdout>Set model to ")
			helpVisible = helpVisible || strings.HasPrefix(text, "<local-command-stdout>Run /model to open the model selection menu")
		}
	}

	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("explicit pinned Source required")
	}
	sourceHome := filepath.Join(root, "source-home")
	_ = os.MkdirAll(sourceHome, 0700)
	_ = os.MkdirAll(filepath.Join(sourceHome, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../testdata/native-model-command.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/loopback-only.sb")
	sourceInput, _ := json.Marshal(map[string]any{"home": sourceHome, "rows": []map[string]any{{"name": "public-help", "args": "help", "settings": map[string]any{"model": "sonnet", "future": true}, "state": map[string]any{"mainLoopModel": "sonnet", "mainLoopModelForSession": nil, "fastMode": false}}}})
	sourceCmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	sourceCmd.Dir = sourceHome
	sourceCmd.Stdin = bytes.NewReader(sourceInput)
	sourceCmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "PATH=/usr/bin:/bin", "CC_HAHA_MODEL_COMMAND_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "ANTHROPIC_API_KEY=owned-Source-model-key", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}
	sourceOut, e := sourceCmd.CombinedOutput()
	if e != nil {
		t.Fatal("actual original local JSX/API normalization", e, string(sourceOut))
	}
	var source struct {
		Rows []struct{ API []struct{ Content any } }
	}
	if e = json.Unmarshal(sourceOut, &source); e != nil || len(source.Rows) != 1 {
		t.Fatal(e, string(sourceOut))
	}
	sourceHelpVisible := false
	for _, message := range source.Rows[0].API {
		content, _ := json.Marshal(message.Content)
		sourceHelpVisible = sourceHelpVisible || strings.Contains(string(content), "Run /model to open the model selection menu")
	}
	if !visibleOutput || helpVisible != sourceHelpVisible {
		t.Fatal("actual typed local result visibility", visibleOutput, helpVisible, sourceHelpVisible)
	}
}
