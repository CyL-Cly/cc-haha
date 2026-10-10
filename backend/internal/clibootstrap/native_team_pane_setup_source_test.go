package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestActualITermSetupOriginalReactCallbacksSelectionInstallVerifyAndPersistedUnknownState(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX original package manager commands")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	binaryScript, err := os.ReadFile("../teamspawn/testdata/iterm_setup.py")
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		// Actual private detection takes longer than the old fixture's 500ms
		// polling budget. Both sides must observe the settled producer before
		// selecting install, without replacing its original async UI callbacks.
		{"config": map[string]any{"available": map[string]bool{"uv": true}, "probeDelaySeconds": 0.65}, "actions": []string{"install", "enter"}},
		{"config": map[string]any{"available": map[string]bool{"uv": true}}, "actions": []string{"tmux"}},
		{"config": map[string]any{"available": map[string]bool{"uv": true}}, "actions": []string{"cancel"}},
		{"config": map[string]any{"available": map[string]bool{"uv": true}}, "actions": []string{"install", "enter"}},
		{"config": map[string]any{"available": map[string]bool{"pipx": true}, "installError": map[string]string{"pipx": "private install failure"}}, "actions": []string{"install", "tmux"}},
		{"config": map[string]any{"available": map[string]bool{"uv": true}, "verifyError": "Python API not enabled"}, "actions": []string{"install", "enter", "tmux"}},
		{"config": map[string]any{}, "actions": []string{"install", "retry", "cancel"}},
	}
	actual := []map[string]any{}
	for _, row := range rows {
		actions := row["actions"].([]string)
		index := 0
		observations := []map[string]any{}
		m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
			n.Environment["TERM_PROGRAM"] = "iTerm.app"
			n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		}, func(o *MainPrintOptions) {
			o.Interactive = true
			o.Arguments.TeammateMode = "tmux"
			o.Arguments.Tools = []string{"Read,TeamCreate,TeamDelete,SendMessage"}
			o.TeamPaneSetup = func(_ context.Context, scope *cooperative.Scope, prompt teamspawn.PaneSetupPrompt) (string, error) {
				if prompt.Step == "clear" || len(prompt.Choices) == 0 {
					return "", nil
				}
				var options any
				if prompt.Step != "api-instructions" {
					items := []map[string]string{}
					for _, choice := range prompt.Choices {
						items = append(items, map[string]string{"label": choice.Label, "value": choice.Value, "description": choice.Description})
					}
					options = items
				}
				observations = append(observations, map[string]any{"step": prompt.Step, "options": options})
				if index >= len(actions) {
					return "", errors.New("unplanned setup choice")
				}
				choice := actions[index]
				index++
				if choice == "enter" {
					choice = "verify"
				}
				return choice, nil
			}
		})
		home := m.options.Native.Home
		canonical, _ := filepath.EvalSymlinks(home)
		home = canonical
		bin := filepath.Join(home, "setup-bin")
		_ = os.Mkdir(bin, 0700)
		log, config := filepath.Join(home, "setup-log"), filepath.Join(home, "setup-config")
		for _, name := range []string{"which", "uv", "pipx", "pip", "pip3", "it2", "tmux"} {
			_ = os.WriteFile(filepath.Join(bin, name), binaryScript, 0755)
		}
		_ = os.WriteFile(config, rawMain(row["config"]), 0600)
		pane, err := teamspawn.NewPaneRuntime(filepath.Join(home, "work"), os.Getpid(), map[string]string{"HOME": home, "PATH": bin + ":/usr/bin:/bin", "TERM_PROGRAM": "iTerm.app", "CC_HAHA_ITERM_SETUP_LOG": log, "CC_HAHA_ITERM_SETUP_CONFIG": config})
		if err != nil {
			t.Fatal(err)
		}
		_ = os.Mkdir(filepath.Join(home, "work"), 0700)
		backend, err := pane.SelectBackend("tmux", false, false)
		if err != nil || backend != "tmux" || !pane.NeedsITermSetup() {
			t.Fatal(backend, err)
		}
		_ = os.WriteFile(log, nil, 0600)
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		var result string
		var failure error
		var saved *protocol.JSONObject
		if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				next.Set("preferTmuxOverIterm2", rawMain(false))
				next.Set("iterm2It2SetupComplete", rawMain(false))
				next.Set("future", rawMain(map[string]bool{"keep": true}))
				return &next, nil
			}); err != nil {
				return err
			}
			m.teamLeader.panes = pane
			result, failure = m.configureNativeTeamPane(ctx, scope, backend)
			var err error
			saved, err = m.Global.Get()
			return err
		}); err != nil {
			t.Fatal(err)
		}
		cancel()
		sourceResult := "installed"
		if result == "tmux" {
			sourceResult = "use-tmux"
		}
		if failure != nil {
			if failure.Error() != "Teammate spawn cancelled - iTerm2 setup required" {
				t.Fatal(failure)
			}
			sourceResult = "cancelled"
		}
		var sourceBackend any
		if result != "" {
			sourceBackend = result
		}
		data, _ := os.ReadFile(log)
		args := []json.RawMessage{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line != "" {
				args = append(args, json.RawMessage(line))
			}
		}
		actual = append(actual, map[string]any{"observations": observations, "result": sourceResult, "backend": sourceBackend, "preferTmux": string(saved.Get("preferTmuxOverIterm2")) == "true", "setupComplete": string(saved.Get("iterm2It2SetupComplete")) == "true", "args": args})
		future, _ := saved.Object("future")
		if string(future.Get("keep")) != "true" {
			t.Fatal("old unknown global state dropped", saved)
		}
	}
	sourceHome, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-iterm-setup.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_TEAM_ITERM_SETUP_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": sourceHome, "binaryScript": string(binaryScript), "ui": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		UI           json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out), stderr.String())
	}
	var a, b any
	_ = json.Unmarshal(rawMain(actual), &a)
	_ = json.Unmarshal(source.UI, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Source React setup differs\nGo=%s\nSource=%s", rawMain(actual), source.UI)
	}
}
