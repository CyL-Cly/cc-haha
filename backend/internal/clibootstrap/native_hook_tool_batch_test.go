package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativePrePostToolHooksModifyInputEnforceRulesAndFeedNextModel(t *testing.T) {
	for _, profile := range []string{"allow", "deny-rule", "ask-rule", "forced-ask", "passthrough"} {
		t.Run(profile, func(t *testing.T) {
			var original, selected, journal string
			var posts atomic.Int32
			var last []byte
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				index := posts.Add(1)
				if index == 1 {
					emitNativeTaskTool(w, "hook-write", "Write", map[string]any{"file_path": original, "content": "original-model-content"})
					return
				}
				last = append([]byte(nil), body...)
				allowed := profile == "allow" || profile == "passthrough"
				if allowed && index == 2 {
					if !strings.Contains(string(body), "pre-hook-context") || !strings.Contains(string(body), "post-hook-context") {
						t.Errorf("actual hook contexts missing next POST: %s", body)
					}
					if strings.Contains(string(body), "ui-only-hook-message") {
						t.Error("Source UI-only Hook system message leaked into API")
					}
					emitNativeTaskTool(w, "hook-read", "Read", map[string]any{"file_path": selected})
					return
				}
				if allowed {
					if !strings.Contains(string(body), "hook-updated-payload") {
						t.Errorf("actual Read result missing: %s", body)
					}
				} else if !strings.Contains(string(body), `"is_error":true`) {
					t.Errorf("rule/forced Hook rejection not fed back: %s", body)
				}
				emitPlanFixture(w, int(index), "", "HOOK_BATCH_DONE")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				original = filepath.Join(n.CWD, "original.txt")
				selected = filepath.Join(n.CWD, "selected.txt")
				journal = filepath.Join(n.CWD, "hook-inputs.jsonl")
				script := filepath.Join(n.CWD, "hook.py")
				code := fmt.Sprintf("import sys,json\nx=json.load(sys.stdin)\nwith open(%q,'a') as f:f.write(json.dumps(x)+'\\n')\ne=x['hook_event_name']\ns={'hookEventName':e,'additionalContext':'pre-hook-context' if e=='PreToolUse' else 'post-hook-context'}\nif e=='PreToolUse':\n if x['tool_name']=='Write':\n  s['updatedInput']={'file_path':%q,'content':'hook-updated-payload'}\n if %q!='passthrough':s['permissionDecision']='ask' if %q=='forced-ask' else 'allow'\nprint(json.dumps({'systemMessage':'ui-only-hook-message','hookSpecificOutput':s}))\n", journal, selected, profile, profile)
				if err := os.WriteFile(script, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				settings := map[string]any{"hooks": map[string]any{}}
				for _, event := range []string{"PreToolUse", "PostToolUse"} {
					settings["hooks"].(map[string]any)[event] = []any{map[string]any{"matcher": "Write|Read", "hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + strconv.Quote(script)}}}}
				}
				if profile == "deny-rule" {
					settings["permissions"] = map[string]any{"deny": []string{"Edit(selected.txt)"}}
				}
				if profile == "ask-rule" {
					settings["permissions"] = map[string]any{"ask": []string{"Write"}}
				}
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) {
				o.Arguments.Tools = []string{"Read,Write"}
				if profile == "ask-rule" || profile == "allow" {
					o.Arguments.PermissionMode = "default"
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "native Pre/Post Hook test"})})); err != nil {
				t.Fatal(err)
			}
			allowed := profile == "allow" || profile == "passthrough"
			if _, err := os.Stat(original); !os.IsNotExist(err) {
				t.Fatal("original model input was executed", err)
			}
			data, err := os.ReadFile(selected)
			if allowed && (err != nil || string(data) != "hook-updated-payload") {
				t.Fatalf("actual modified Write/Read %q %v", data, err)
			}
			if !allowed && !os.IsNotExist(err) {
				t.Fatal("Hook approval bypassed rule/ask", string(data), err)
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			want := 1
			if allowed {
				want = 4
			}
			if len(lines) != want {
				t.Fatalf("actual processes %d !=%d %s", len(lines), want, data)
			}
			for i, line := range lines {
				var x protocol.JSONObject
				if err = json.Unmarshal([]byte(line), &x); err != nil {
					t.Fatal(err)
				}
				if x.String("session_id") != m.Controller.SessionID() || x.Get("stop_hook_active") != nil {
					t.Fatal("Hook lost actual session/input identity", line)
				}
				if i%2 == 1 {
					if x.Get("tool_response") == nil {
						t.Fatal("Post Hook did not consume physical Tool result", line)
					}
				}
			}
			if len(last) == 0 {
				t.Fatal("actual shared Harness did not continue")
			}
		})
	}
}
