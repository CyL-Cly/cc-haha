package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualShellHintDoesNotReachModelAndTerminalInstallsSharedPlugin(t *testing.T) {
	for _, foreground := range []bool{false, true} {
		t.Run(map[bool]string{false: "print", true: "foreground"}[foreground], func(t *testing.T) {
			var count atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				round := count.Add(1)
				if round == 1 {
					emitNativeTaskTool(w, "actual-hint-shell", "Bash", map[string]any{"command": `printf 'MODEL_OUTPUT\n<claude-code-hint v=1 type=plugin value=sample@agent-skills/>\nTAIL\n'`})
					return
				}
				var request struct{ Messages []protocol.JSONObject }
				_ = json.Unmarshal(body, &request)
				found := false
				for _, message := range request.Messages {
					for _, block := range taskBlocks(message.Get("content")) {
						if block.String("type") == "tool_result" {
							found = true
							text := string(block.Get("content"))
							if strings.Contains(text, "<claude-code-hint") || !strings.Contains(text, "MODEL_OUTPUT") || !strings.Contains(text, "TAIL") {
								t.Errorf("hint/model shell boundary %s", rawMain(block))
							}
						}
					}
				}
				if !found {
					t.Error("no actual model tool-result consumer")
				}
				emitPlanFixture(w, int(round), "", "HINT_PHYSICAL_DONE")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {

				market := filepath.Join(n.Home, "owned-hint-market")
				for file, content := range map[string]string{".claude-plugin/marketplace.json": `{"name":"agent-skills","owner":{"name":"Owned"},"plugins":[{"name":"sample","description":"Actual hint plugin","source":"./plugins/sample"}]}`, "plugins/sample/.claude-plugin/plugin.json": `{"name":"sample","version":"1.0.0"}`, "plugins/sample/skills/hint-skill/SKILL.md": "---\ndescription: Physical hint installed skill\n---\nActual owned skill file."} {
					path := filepath.Join(market, file)
					if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
						t.Fatal(e)
					}
					if e := os.WriteFile(path, []byte(content), 0600); e != nil {
						t.Fatal(e)
					}
				}
				known := map[string]any{"agent-skills": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2026-10-01T00:00:00.000Z"}}
				if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "plugins", "known_marketplaces.json"), rawMain(known), 0600); e != nil {
					t.Fatal(e)
				}
			}, func(o *MainPrintOptions) { o.ForegroundShellProgress = foreground; o.Interactive = true })
			if e := m.Owner.Run(context.Background(), func(*cooperative.Scope) error {
				m.nativeHintRuntimeOnOwner().featureEnabled = func(map[string]string, protocol.JSONObject) bool { return true }
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			hints := make(chan *NativePluginHint, 8)
			initial, remove, e := m.SubscribePluginHints(ctx, func(h *NativePluginHint) { hints <- h })
			if e != nil || initial != nil {
				t.Fatal(initial, e)
			}
			defer remove()
			if e = m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run the owned hint command"})})); e != nil {
				t.Fatal(e)
			}
			select {
			case hint := <-hints:
				if hint == nil || hint.Name != "sample" || hint.Marketplace != "agent-skills" || hint.SourceCommand != "printf" {
					t.Fatal(hint)
				}
			case <-ctx.Done():
				t.Fatal("actual native recommendation did not resolve", ctx.Err())
			}
			notice, e := m.RespondPluginHint(ctx, "yes")
			if e != nil || notice != "✓ sample installed · restart to apply" {
				t.Fatal(notice, e)
			}
			installed, e := m.nativeSettings.plugins.installed.Disk()
			if e != nil {
				t.Fatal(e)
			}
			entries := nativeInProcessObject(installed.Get("plugins"))
			var installs []protocol.JSONObject
			_ = json.Unmarshal(entries.Get("sample@agent-skills"), &installs)
			if len(installs) != 1 || installs[0].String("scope") != "user" {
				t.Fatalf("actual install record %s", rawMain(installed))
			}
			content, e := os.ReadFile(filepath.Join(installs[0].String("installPath"), "skills/hint-skill/SKILL.md"))
			if e != nil || !strings.Contains(string(content), "Actual owned skill file") {
				t.Fatal("physical materialization", e, string(content))
			}
			global, e := m.Global.Get()
			if e != nil || !strings.Contains(string(global.Get("claudeCodeHints")), "sample@agent-skills") {
				t.Fatal("show-once persistence", global, e)
			}
			if count.Load() != 2 {
				t.Fatal("hint recommendation triggered inference", count.Load())
			}
		})
	}
}
