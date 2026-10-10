package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualSkillValidationErrorsPreserveSourceTwoJSONOutlets(t *testing.T) {
	for _, row := range []struct {
		label              string
		pluginNames, names []json.RawMessage
		codes              []int
	}{
		{"lone-identity", []json.RawMessage{json.RawMessage(`"same-\ud800"`), json.RawMessage(`"same-\udc00"`)}, []json.RawMessage{json.RawMessage(`" \ufeff/missing-\ud800:reader\u3000 "`), json.RawMessage(`"same-\ud800:reader"`), json.RawMessage(`"missing-\udc00:reader"`)}, []int{2, 4, 2}},
		{"ordinary-four-validation-branches", []json.RawMessage{json.RawMessage(`"ordinary"`)}, []json.RawMessage{json.RawMessage(`" \ufeff\u3000 "`), json.RawMessage(`" /missing-normal "`), json.RawMessage(`"ordinary:reader"`), json.RawMessage(`"/cost"`)}, []int{1, 2, 4, 5}},
	} {
		t.Run(row.label, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			f.write(t, filepath.Join(f.home, ".git", "HEAD"), "ref: refs/heads/private\n")
			f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/private\n")
			dirs := []string{}
			for i, name := range row.pluginNames {
				dir := filepath.Join(f.home, fmt.Sprintf("plugin-%d", i))
				dirs = append(dirs, dir)
				f.write(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":`+string(name)+`}`)
				f.write(t, filepath.Join(dir, "skills", "reader", "SKILL.md"), fmt.Sprintf("---\ndescription: Actual disabled body %d\ndisable-model-invocation: true\n---\nMUST_NOT_EXECUTE_BODY_%d", i, i))
			}
			var mu sync.Mutex
			requests := []json.RawMessage{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "fake-validation-utf16" {
					t.Error("actual peer credential changed")
					http.Error(w, "unauthenticated", 401)
					return
				}
				body, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
					return
				}
				mu.Lock()
				requests = append(requests, append(json.RawMessage(nil), body...))
				n := len(requests)
				mu.Unlock()
				if n <= len(row.names) {
					emitNativeTaskTool(w, fmt.Sprintf("actual-validation-skill-%d", n), "Skill", map[string]any{"skill": row.names[n-1]})
				} else if n == len(row.names)+1 {
					emitPlanFixture(w, n, "", "VALIDATION_DONE")
				} else {
					t.Error("unexpected actual POST", n)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			f.n.Environment["ANTHROPIC_API_KEY"] = "fake-validation-utf16"
			f.n.Environment["PATH"] = "/usr/bin:/bin"
			f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			disabled := false
			var diagnostics bytes.Buffer
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual private Skill validation", Tools: []string{"Skill"}, PluginDirectories: dirs, MaxTurns: len(row.names) + 1, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics})
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if e = m.StartSession(ctx); e != nil {
				t.Fatal(e)
			}
			defer m.ShutdownSession(context.Background())
			user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Validate original Skill branches"})})
			if e = m.RunTurn(ctx, user); e != nil {
				t.Fatal(e, diagnostics.String())
			}
			mu.Lock()
			goRequests := append([]json.RawMessage(nil), requests...)
			mu.Unlock()
			if len(goRequests) != len(row.names)+1 {
				t.Fatal("actual next request missing", len(goRequests))
			}
			fixture, _ := filepath.Abs("testdata/skill-validation-error-source.fixture.ts")
			sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
			child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
			child.Dir = f.home
			child.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_GUIDE_UTF16_FIXTURE=1", "ANTHROPIC_API_KEY=fake-validation-utf16", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file"}
			child.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "cwd": f.cwd, "directories": dirs, "names": row.names, "goRequests": goRequests, "goHistory": m.Controller.History()}))
			var stderr bytes.Buffer
			child.Stderr = &stderr
			data, e := child.Output()
			if e != nil {
				t.Fatal("actual Source validateInput/runToolUse/CreateUserMessage", e, stderr.String())
			}
			var actual protocol.JSONObject
			if e = actual.UnmarshalJSON(data); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(t.TempDir(), "skill-validation-"+row.label+"-actual-evidence.json"), append(data, '\n'), 0600); e != nil {
				t.Fatal(e)
			}
			if string(actual.Get("exactValidationParity")) != "true" {
				t.Fatalf("Source and actual model/history error JSON differ\nSource%s\nGo%s", actual.Get("sourceResults"), actual.Get("goResults"))
			}
			if !bytes.Equal(actual.Get("validationCodes"), rawMain(row.codes)) {
				t.Fatal("Original four validation branches changed", string(actual.Get("validationCodes")))
			}
			// Validation must neither invoke a command nor update its usage/invoked state.
			if strings.Contains(string(rawMain(goRequests)), "MUST_NOT_EXECUTE_BODY_") {
				t.Fatal("validation executed disabled body")
			}
		})
	}
}
