package conversationapi

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSDKCaptureActualSourceReplayEvictionMalformedAndRetention(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	payloads := []string{`{"type":"system","subtype":"init","uuid":"init","cwd":"private"}`, ` {"type":"assistant","uuid":"one","message":{"content":[{"type":"text","text":"original"}]}} `, "invalid\n{\"type\":\"assistant\",\"uuid\":\"one\"}\n{\"type\":\"system\",\"subtype\":\"session_state_changed\",\"state\":\"idle\"}"}
	for i := 0; i < 2001; i++ {
		message := obj(field("type", "progress"), field("uuid", string(rune('A'+i))), field("content", "actual"))
		payloads = append(payloads, string(mustSDKObject(message)))
	}
	messages := []protocol.JSONObject{
		obj(field("type", "assistant"), field("message", map[string]any{"content": []map[string]string{{"type": "text", "text": strings.Repeat("😀", 20000)}}}), field("status", "private "+strings.Repeat("e", 68000))),
		obj(field("type", "result"), field("result", strings.Repeat("中", 30000)), field("errors", []any{strings.Repeat("😀", 3000), 7, "plain"})),
		obj(field("type", 2), field("data", strings.Repeat("x", 70000))),
	}
	for _, data := range messages {
		payloads = append(payloads, string(mustSDKObject(data)))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-capture.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_CAPTURE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "payloads": payloads})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Recent, Forwarded []json.RawMessage
		Init              json.RawMessage
		Seen              int
		Capabilities      struct{ Blocked []string }
	}
	if e := json.Unmarshal(out, &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("invalid Source %v blocked %v %.300s", e, source.Capabilities.Blocked, out)
	}
	capture := NewSDKCapture()
	forwarded := []json.RawMessage{}
	for _, payload := range payloads {
		forwarded = append(forwarded, capture.Parse(payload)...)
	}
	compare := func(name string, actual, expected any) {
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(expected)
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b)) {
			t.Errorf("%s Source%.1500s Go%.1500s", name, b, a)
		}
	}
	compare("recent", capture.Recent(), source.Recent)
	compare("init", capture.Init(), source.Init)
	compare("forwarded", forwarded, source.Forwarded)
	if len(capture.seen) != source.Seen {
		t.Fatal("replay eviction", len(capture.seen), source.Seen)
	}
}
