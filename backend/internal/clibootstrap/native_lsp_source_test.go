package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestActualSourceInstalledPluginLSPColdAndHotFileFrames(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source Bun required")
	}
	for _, hot := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold", true: "hot"}[hot], func(t *testing.T) {
			f := newNativePluginFixture(t)
			peer, receipt := filepath.Join(f.plugin, "bin", "private-lsp-peer"), filepath.Join(f.home, "actual-lsp-frames.jsonl")
			data, err := os.ReadFile("../../testdata/lsp-file-notify-peer.py")
			if err != nil {
				t.Fatal(err)
			}
			f.write(t, peer, string(data))
			if err = os.Chmod(peer, 0700); err != nil {
				t.Fatal(err)
			}
			f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.0.0"}`)
			config := protocol.NewJSONObject(fieldMain("command", peer), fieldMain("args", []string{receipt, f.home}), fieldMain("workspaceFolder", f.n.CWD), fieldMain("extensionToLanguage", map[string]string{".txt": "plaintext"}))
			f.write(t, filepath.Join(f.plugin, ".lsp.json"), string(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "fixture", Value: rawMain(config)}))))
			installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(f.config, "plugins"), Store: storage.New(storage.Options{})})
			if err != nil {
				t.Fatal(err)
			}
			if err = installed.Add(context.Background(), "sample@mkt", "user", f.plugin, mainObj(json.RawMessage(`{"name":"sample","version":"1.0.0"}`)), nil); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(f.n.CWD, "actual space 中文.txt")
			f.write(t, file, "old\nline\n")
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-lsp-runtime.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = f.home
			cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_LSP_RUNTIME_SOURCE=1", "NODE_ENV=production", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false", "ANTHROPIC_API_KEY=private-only-lsp", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "cwd": f.n.CWD, "peer": peer, "receipt": receipt, "file": file, "content": "new\nline\n", "hot": hot}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal("actual Source plugin LSP", err, string(output))
			}
			var result struct {
				Initial      []protocol.JSONObject
				Frames       []protocol.JSONObject
				Capabilities struct{ Blocked, SecureReads []string }
			}
			if err = json.Unmarshal(output, &result); err != nil {
				t.Fatal(err, string(output))
			}
			if len(result.Initial) != 1 || result.Initial[0].String("state") != "stopped" || len(result.Capabilities.Blocked) != 0 || len(result.Capabilities.SecureReads) != 0 {
				t.Fatal("original lazy/private LSP premise", string(output))
			}
			methods := []string{}
			var notified []string
			for _, row := range result.Frames {
				method := row.String("method")
				methods = append(methods, method)
				if method == "textDocument/didChange" || method == "textDocument/didSave" {
					notified = append(notified, method)
					if row.String("disk") != "new\nline\n" {
						t.Fatal("original notification preceded write", string(rawMain(row)))
					}
				}
			}
			want := []string(nil)
			if hot {
				want = []string{"textDocument/didChange", "textDocument/didSave"}
			}
			if !reflect.DeepEqual(notified, want) {
				t.Fatal("actual original notification frames", hot, methods, string(output))
			}
			if len(methods) < 4 || methods[0] != "initialize" || methods[1] != "initialized" {
				t.Fatal("original LSP startup protocol", methods)
			}
			t.Logf("actual Source hot=%v frames=%v", hot, methods)
		})
	}
}
