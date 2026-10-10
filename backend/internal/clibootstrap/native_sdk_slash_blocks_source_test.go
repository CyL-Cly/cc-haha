package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Exact bytes of the authorized saved P868 workflow; no user profile reads at test time.
const actualSDKSlashSavedWorkflowP868 = `export const meta = { name: 'native-p868-readers', description: 'Live completion save verification', phases: [{ title: 'Read', detail: 'Read two physical inputs' }] };
phase('Read');
const results = await parallel([() => agent('实际用 Read 读取当前目录 native-workflow-input-P868-a.txt，报告完整第一行 marker 和第二行整数之和；不得修改文件。', {label: 'Reader A'}), () => agent('实际用 Read 读取当前目录 native-workflow-input-P868-b.txt，报告完整第一行 marker 和第二行整数之和；不得修改文件。', {label: 'Reader B'})]);
log(JSON.stringify(results));`

func actualSDKSlashInputSource(t *testing.T, home, cwd string, input any) protocol.JSONObject {
	t.Helper()
	value := map[string]json.RawMessage{}
	data, _ := json.Marshal(input)
	_ = json.Unmarshal(data, &value)
	value["homeDir"] = quoteMain(home)
	value["cwd"] = quoteMain(cwd)
	data, _ = json.Marshal(value)
	fixture, _ := filepath.Abs("testdata/sdk_slash_blocks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_SLASH_BLOCKS=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "ANTHROPIC_API_KEY=fake-only-loopback", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_ATTACHMENTS=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source processUserInput %v %.2200s", err, stderr.String())
	}
	var result protocol.JSONObject
	if err = result.UnmarshalJSON(out); err != nil {
		t.Fatal(err, string(out))
	}
	if string(result.Get("blocked")) != "[]" {
		t.Fatal("Source escaped fixture", result.Get("blocked"))
	}
	return result
}

func TestActualMainPrintSDKContentBlockSlashMatchesOriginalProcessUserInput(t *testing.T) {
	for _, kind := range []string{"saved-workflow", "skill", "skill-preceding", "skill-history", "skill-noargs", "skill-quoted-args", "skill-unicode-args", "model-only", "plain-multiple", "non-final-slash"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			requests := [][]byte{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, data)
				mu.Unlock()
				emitPlanFixture(w, 1, "", "ACTUAL_SLASH_INPUT_DONE")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				_ = os.MkdirAll(filepath.Join(n.CWD, ".git"), 0700)
				for _, dir := range []string{filepath.Join(n.CWD, ".claude", "workflows"), filepath.Join(n.CWD, ".claude", "skills", "plain")} {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				saved := []byte(actualSDKSlashSavedWorkflowP868)
				var err error
				if err = os.WriteFile(filepath.Join(n.CWD, ".claude", "workflows", "native-p868-readers.js"), saved, 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.MkdirAll(filepath.Join(n.CWD, ".claude", "skills", "modelonly"), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(n.CWD, ".claude", "skills", "modelonly", "SKILL.md"), []byte("---\ndescription: Actual model only\nuser-invocable: false\n---\nProtected model-only prompt."), 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(n.CWD, ".claude", "skills", "plain", "SKILL.md"), []byte("---\ndescription: Actual plain skill\n---\nActual skill body: $ARGUMENTS."), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) {
				o.Arguments.Tools = []string{"Workflow,Skill,Read,Write,TaskOutput,Agent"}
				o.Arguments.PermissionMode = "default"
			})
			text := func(value string) protocol.JSONObject {
				return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("text")}, protocol.JSONField{Name: "text", Value: quoteMain(value)})
			}
			var blocks []protocol.JSONObject
			switch kind {
			case "saved-workflow":
				blocks = []protocol.JSONObject{text("/native-p868-readers")}
			case "skill":
				blocks = []protocol.JSONObject{text("/plain literal arguments")}
			case "skill-noargs":
				blocks = []protocol.JSONObject{text("/plain")}
			case "skill-quoted-args":
				blocks = []protocol.JSONObject{text("ACTUAL_PRECEDING_BLOCK"), text(`/plain <&> "two words" \literal`)}
			case "skill-unicode-args":
				blocks = []protocol.JSONObject{text("/plain 尾部 🧭 café \u2028middle\u2029 end")}
			case "skill-preceding", "skill-history":
				blocks = []protocol.JSONObject{text("ACTUAL_PRECEDING_BLOCK"), text("/plain args 27")}
			case "model-only":
				blocks = []protocol.JSONObject{text("ACTUAL_MODEL_ONLY_PRECEDING"), text("/modelonly")}
			case "plain-multiple":
				blocks = []protocol.JSONObject{text("First normal block"), text("Second normal block")}
			case "non-final-slash":
				blocks = []protocol.JSONObject{text("/plain is preceding text"), text("Normal final block")}
			}
			content := rawMain(blocks)
			uuid := "11111111-2222-4333-8444-555555555555"
			previous := []protocol.JSONObject{}
			if kind == "skill-history" {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain("ACTUAL_NONEMPTY_PRIOR_HISTORY")}))})); err != nil {
					cancel()
					t.Fatal(err)
				}
				cancel()
				previous = m.Controller.History()
			}
			beforeHistory := rawMain(previous)
			source := actualSDKSlashInputSource(t, m.options.Native.Home, m.options.Native.CWD, map[string]any{"content": blocks, "uuid": uuid, "permissionMode": "default", "history": previous})
			if !planSourcesEqual(source.Get("originalContent"), content) {
				t.Fatal("Source fixture changed original blocks", source.Get("originalContent"), content)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuid)}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: content}))})
			if err := m.RunTurn(ctx, input); err != nil {
				t.Fatalf("actual MainPrint raw SDK content blocks rejected while Source shouldQuery=%s: %v", source.Get("shouldQuery"), err)
			}
			history := m.Controller.History()
			if len(previous) > 0 && !bytes.Equal(rawMain(history[:len(previous)]), beforeHistory) {
				t.Fatal("preceding history mutated")
			}
			history = history[len(previous):]
			projected := []protocol.JSONObject{}
			for _, row := range history {
				if row.String("type") == "assistant" {
					break
				}
				row = row.Clone()
				row.Delete("uuid")
				row.Delete("timestamp")
				projected = append(projected, row)
			}
			if !planSourcesEqual(rawMain(projected), source.Get("messages")) {
				t.Fatalf("actual SDK process input differs\nGo=%s\nSource=%s", rawMain(projected), source.Get("messages"))
			}
			mu.Lock()
			defer mu.Unlock()
			wantPosts := 1
			if kind == "model-only" {
				wantPosts = 0
			}
			if kind == "skill-history" {
				wantPosts = 2
			}
			if len(requests) != wantPosts {
				t.Fatal("wrong real model request count", len(requests))
			}
			if kind == "saved-workflow" && !strings.Contains(string(requests[0]), "Run the saved workflow") {
				t.Fatal("saved Workflow not expanded at actual model boundary")
			}
		})
	}
}

func TestActualMainPrintSDKSlashBlockStdinEOFAndClose(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "Actual EOF Skill body") {
			t.Error("EOF input not expanded by original skill producer")
		}
		emitPlanFixture(w, 1, "", "ACTUAL_EOF_SLASH_DONE")
	}))
	defer peer.Close()
	capture := &taskPublicationCapture{}
	input := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"/eof-skill"}]}}` + "\n"
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		folder := filepath.Join(n.CWD, ".claude", "skills", "eof-skill")
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte("---\ndescription: Actual EOF skill\n---\nActual EOF Skill body."), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Input = strings.NewReader(input)
		o.Arguments.PermissionMode = "default"
		o.OnOutput = capture.enqueue
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := m.Run(ctx); err != nil {
		t.Fatal("actual raw block slash EOF", err)
	}
	result := false
	for _, row := range capture.snapshot() {
		if row.String("type") == "result" && row.String("result") == "ACTUAL_EOF_SLASH_DONE" {
			result = true
		}
	}
	if !result {
		t.Fatal("EOF lost actual result", rawMain(capture.snapshot()))
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal("repeat Close after EOF", err)
	}
}
