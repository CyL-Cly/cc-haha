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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestActualPluginAgentPreloadUsesParentCommandMemoUntilExplicitReload(t *testing.T) {
	files := []map[string]string{
		{"path": "commands/late-command.md", "content": "---\ndescription: Late command description\n---\nPLUGIN_MEMO_LATE_COMMAND_BODY"},
		{"path": "skills/late-skill/SKILL.md", "content": "---\ndescription: Late skill description\n---\nPLUGIN_MEMO_LATE_SKILL_BODY"},
	}
	seed := func() nativePluginFixture {
		f := newNativePluginFixture(t)
		f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.0.0"}`)
		f.write(t, filepath.Join(f.plugin, "agents", "worker.md"), "---\nname: worker\ndescription: Actual memo worker\ntools: Read\nmodel: inherit\nskills: [sample:late-command, sample:late-skill]\n---\nPLUGIN_MEMO_AGENT")
		installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(f.config, "plugins"), Store: storage.New(storage.Options{})})
		if err != nil {
			t.Fatal(err)
		}
		manifest := mainObj(json.RawMessage(`{"name":"sample","version":"1.0.0","installedAt":"2026-10-01T00:00:00.000Z"}`))
		if err := installed.Add(context.Background(), "sample@mkt", "user", f.plugin, manifest, nil); err != nil {
			t.Fatal(err)
		}
		f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
		f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
		f.n.Environment["CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION"] = "false"
		return f
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	source := seed()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-plugin-memo.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = source.home
	command.Env = []string{"HOME=" + source.home, "USERPROFILE=" + source.home, "CLAUDE_CONFIG_DIR=" + source.config, "TMPDIR=" + source.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(source.home), "CC_HAHA_PLUGIN_AGENT_MEMO=1", "NODE_ENV=production", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false", "ANTHROPIC_API_KEY=private-memo-source-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"home": source.home, "cwd": source.n.CWD, "plugin": source.plugin, "files": files}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source Agent memo", err, string(output))
	}
	var expected struct {
		Rows []struct {
			Reloaded bool
			Messages json.RawMessage
		}
	}
	if err := json.Unmarshal(output, &expected); err != nil || len(expected.Rows) != 2 {
		t.Fatal("actual Source Agent rows", err, string(output))
	}
	for _, background := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreground", true: "background"}[background], func(t *testing.T) {
			f := seed()
			f.n.Environment["ANTHROPIC_API_KEY"] = "private-memo-native-key"
			var mu sync.Mutex
			parent, child, issued := 0, 0, 0
			var actualRequests []json.RawMessage
			childReady := make(chan struct{}, 2)
			observed := [][2]bool{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var request protocol.JSONObject
				if err := request.UnmarshalJSON(body); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				actualRequests = append(actualRequests, append(json.RawMessage(nil), body...))
				if bytes.Contains(request.Get("system"), []byte("PLUGIN_MEMO_AGENT")) {
					child++
					observed = append(observed, [2]bool{bytes.Contains(request.Get("messages"), []byte("PLUGIN_MEMO_LATE_COMMAND_BODY")), bytes.Contains(request.Get("messages"), []byte("PLUGIN_MEMO_LATE_SKILL_BODY"))})
					emitPlanFixture(w, child, "", "PLUGIN_MEMO_CHILD_DONE")
					select {
					case childReady <- struct{}{}:
					default:
					}
				} else {
					parent++
					// Background completion can wake the actual headless root and
					// make another ordinary ASK. Only each new human requests an
					// Agent; its later task notification must not recursively spawn.
					humans := bytes.Count(request.Get("messages"), []byte("Use actual sample:worker preset"))
					if humans > issued {
						issued = humans
						emitNativeTaskTool(w, "plugin-memo-agent", "Agent", map[string]any{"description": "Use installed memo worker", "prompt": "Consume only available preloaded skills", "subagent_type": "sample:worker", "run_in_background": background})
					} else {
						emitPlanFixture(w, parent, "", "PLUGIN_MEMO_PARENT_DONE")
					}
				}
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "PLUGIN_MEMO_PARENT", Tools: []string{"Agent,Read"}, Sources: DefaultState(), MaxTurns: 8}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "fixture", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			// Parent construction resolves its real command catalog before disk changes.
			for _, row := range files {
				f.write(t, filepath.Join(f.plugin, filepath.FromSlash(row["path"])), row["content"])
			}
			if err := m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			for i, row := range expected.Rows {
				if row.Reloaded {
					if _, err := m.ReloadComponents(ctx); err != nil {
						t.Fatal(err)
					}
				}
				input := objMain(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Use actual sample:worker preset"}}))
				if err := m.RunTurn(ctx, input); err != nil {
					t.Fatal(err)
				}
				select {
				case <-childReady:
				case <-ctx.Done():
					t.Fatal("actual configured child HTTP did not arrive", ctx.Err())
				}
				mu.Lock()
				count, parentRequests, childRequests := len(observed), parent, child
				var got [2]bool
				if count == i+1 {
					got = observed[i]
				}
				mu.Unlock()
				if count != i+1 {
					t.Fatalf("actual Agent HTTP count=%d parent=%d child=%d requests=%s", count, parentRequests, childRequests, rawMain(actualRequests))
				}
				want := [2]bool{bytes.Contains(row.Messages, []byte("PLUGIN_MEMO_LATE_COMMAND_BODY")), bytes.Contains(row.Messages, []byte("PLUGIN_MEMO_LATE_SKILL_BODY"))}
				if got != want {
					t.Fatalf("actual Agent preload bypassed parent memo: reloaded=%v Go=%v Source=%v", row.Reloaded, got, want)
				}
				if want != [2]bool{row.Reloaded, row.Reloaded} {
					t.Fatal("actual Source warm/reload premise changed", string(output))
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if parent < 4 || !background && parent != 4 || child != 2 || issued != 2 {
				t.Fatal("actual parent/child model consumers", parent, child)
			}
		})
	}
}
