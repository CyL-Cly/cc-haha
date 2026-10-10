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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualLonePluginSkillInvocationIdentityMatchesSource(t *testing.T) {
	runActualLonePluginSkillIdentity(t, false, false)
}
func TestActualLonePluginSkillPhysicalSettingsDenyMatchesSource(t *testing.T) {
	runActualLonePluginSkillIdentity(t, true, false)
}
func TestActualLonePluginSkillPhysicalRuleReloadMatchesSource(t *testing.T) {
	runActualLonePluginSkillIdentity(t, true, true)
}
func runActualLonePluginSkillIdentity(t *testing.T, denied, reload bool) {
	f := newNativePluginFixture(t)
	settings := `{"enabledPlugins":{"sample@mkt":false}}`
	if denied {
		settings = `{"enabledPlugins":{"sample@mkt":false},"permissions":{"deny":["Skill(same-\ud800:reader)"]}}`
	}
	f.write(t, filepath.Join(f.config, "settings.json"), settings)
	f.write(t, filepath.Join(f.home, ".git", "HEAD"), "ref: refs/heads/private\n")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/private\n")
	names := []json.RawMessage{json.RawMessage(`"same-\ud800:reader"`), json.RawMessage(`"same-\udc00:reader"`), json.RawMessage(`" \ufeff/same-\ud800:reader\u3000 "`)}
	dirs := []string{}
	for i, name := range []string{`"same-\ud800"`, `"same-\udc00"`} {
		dir := filepath.Join(f.home, fmt.Sprintf("plugin-%d", i))
		dirs = append(dirs, dir)
		f.write(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":`+name+`}`)
		f.write(t, filepath.Join(dir, "skills", "reader", "SKILL.md"), fmt.Sprintf("---\ndescription: Actual different body %d\n---\nPRIVATE_LOOKUP_BODY_%d", i, i))
	}
	var mu sync.Mutex
	requests := []json.RawMessage{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "fake-lookup-utf16" {
			t.Error("actual peer credential changed")
			http.Error(w, "unauthenticated", 401)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, append(json.RawMessage(nil), body...))
		n := len(requests)
		mu.Unlock()
		switch n {
		case 1, 2, 3, 5, 6, 7:
			index := n - 1
			id := n
			if n > 4 {
				index = n - 5
				id = n - 1
			}
			emitNativeTaskTool(w, fmt.Sprintf("actual-lone-skill-%d", id), "Skill", map[string]any{"skill": names[index]})
		case 4, 8:
			emitPlanFixture(w, n, "", "PRIVATE_LOOKUP_DONE")
		default:
			http.Error(w, "unexpected", 500)
			t.Error("unexpected actual Skill POST", n)
		}
	}))
	defer peer.Close()
	oldConfig := `{"future":{"keep":true},"skillUsage":{"old-normal":{"usageCount":4,"lastUsedAt":12}}}`
	f.write(t, filepath.Join(f.config, ".claude.json"), oldConfig)
	f.n.NowMillis = func() float64 { return 100000 }
	f.n.Now = func() time.Time { return time.UnixMilli(100000) }
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-lookup-utf16"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	disabled := false
	var diagnostic bytes.Buffer
	if denied {
		fixture, _ := filepath.Abs("testdata/skill-lone-identity-source.fixture.ts")
		sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
		child := exec.Command("/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
		child.Dir = f.home
		child.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_GUIDE_UTF16_FIXTURE=1", "ANTHROPIC_API_KEY=fake-lookup-utf16", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file"}
		child.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "cwd": f.cwd, "directories": dirs, "names": names, "denied": true, "permissionOnly": true}))
		var stderr bytes.Buffer
		child.Stderr = &stderr
		source, e := child.Output()
		if e != nil {
			t.Fatal("actual Source physical deny initializer", e, stderr.String())
		}
		var actual struct{ Decisions []struct{ Behavior string } }
		if e = json.Unmarshal(source, &actual); e != nil {
			t.Fatal(e)
		}
		if len(actual.Decisions) != 3 || actual.Decisions[0].Behavior != "deny" || actual.Decisions[1].Behavior != "allow" || actual.Decisions[2].Behavior != "deny" {
			t.Fatal("Original physical permission domain missing", string(source))
		}
		if e = os.WriteFile(filepath.Join(t.TempDir(), "skill-physical-settings-source-permission.json"), source, 0600); e != nil {
			t.Fatal(e)
		}
	}
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual private Skill lookup", Tools: []string{"Skill"}, PluginDirectories: dirs, MaxTurns: 4, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostic})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownSession(context.Background())
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Load both exact raw Skills"})})
	if err = m.RunTurn(ctx, user); err != nil {
		t.Fatal(err, diagnostic.String())
	}
	if reload {
		before, e := m.Global.Get()
		if e != nil {
			t.Fatal(e)
		}
		oldConfig = string(rawMain(before))
		f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"permissions":{"deny":["Skill(same-\udc00:reader)"]}}`)
		if _, e = m.ReloadComponents(ctx); e != nil {
			t.Fatal("actual control reload", e)
		}
		if e = m.RunTurn(ctx, user); e != nil {
			t.Fatal("warm actual Skill turn", e, diagnostic.String())
		}
	}

	mu.Lock()
	expectedPosts := 4
	if reload {
		expectedPosts = 8
	}
	if len(requests) != expectedPosts {
		mu.Unlock()
		t.Fatal("actual Skill sequence missing", len(requests))
	}
	goBodies := append([]json.RawMessage(nil), requests...)
	if reload {
		goBodies = goBodies[4:]
	}
	mu.Unlock()
	var commandMetadata []protocol.JSONObject
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		commands, err := m.nativeSkillCommands(scope)
		if err != nil {
			return err
		}
		for _, command := range commands {
			commandMetadata = append(commandMetadata, command.Metadata)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	goConfig, err := m.Global.Get()
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, filepath.Join(f.config, ".claude.json"), oldConfig)
	fixture, _ := filepath.Abs("testdata/skill-lone-identity-source.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
	child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	child.Dir = f.home
	child.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_GUIDE_UTF16_FIXTURE=1", "ANTHROPIC_API_KEY=fake-lookup-utf16", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file"}
	child.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "cwd": f.cwd, "directories": dirs, "names": names, "goRequests": goBodies, "goNativeCommandMetadata": commandMetadata, "goConfig": goConfig, "oldConfig": oldConfig, "denied": denied, "reload": reload}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, err := child.Output()
	if err != nil {
		t.Fatal("actual Source Skill lookup/invocation", err, stderr.String())
	}
	var actual protocol.JSONObject
	if err = actual.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(t.TempDir(), "skill-lone-identity-actual-evidence.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if string(actual.Get("exactUsageParity")) != "true" {
		t.Errorf("actual original usage config mismatch Source=%s Go=%s", actual.Get("sourceConfig"), actual.Get("goConfig"))
	}
	if string(actual.Get("exactInvocationParity")) != "true" {
		t.Fatalf("actual Source accepted/selected/invoked raw Skill; Go actual model tool consumer rejected or selected different body\nSource%s\nGo%s", actual.Get("sourceResults"), actual.Get("goResults"))
	}
}
