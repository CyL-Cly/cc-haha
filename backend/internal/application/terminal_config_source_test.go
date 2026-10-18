package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTerminalConfigNativeWritesMatchActualOriginalSettingCallbacks(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type sourceCase struct {
		ID        string            `json:"id"`
		Value     any               `json:"value"`
		Global    any               `json:"global"`
		User      any               `json:"user"`
		Local     any               `json:"local"`
		Env       map[string]string `json:"env"`
		Supported bool              `json:"supported"`
	}
	rows := []sourceCase{}
	results := []protocol.JSONObject{}
	for _, pair := range []struct {
		id    string
		value any
	}{{"autoCompactEnabled", false}, {"spinnerTipsEnabled", false}, {"prefersReducedMotion", true}, {"thinkingEnabled", false}, {"thinkingEnabled", true}, {"instructionFiles", "managed-only"}, {"workflows", false}, {"workflowKeywordTriggerEnabled", false}, {"workflowSizeGuideline", "small"}, {"verbose", true}, {"terminalProgressBarEnabled", false}, {"showTurnDuration", false}, {"defaultPermissionMode", "plan"}, {"respectGitignore", false}, {"copyFullResponse", true}, {"notifChannel", "notifications_disabled"}, {"editorMode", "vim"}, {"apiKey", true}, {"apiKey", false}, {"autoConnectIde", true}, {"autoInstallIdeExtension", false}, {"claudeInChromeDefaultEnabled", true}, {"claudeInChromeDefaultEnabled", false}} {
		o := nativeSessionOptions(t, "http://127.0.0.1:1")
		o.Interactive = true
		if pair.id == "autoInstallIdeExtension" {
			for _, environment := range []map[string]string{o.Environment, o.Native.Environment} {
				environment["TERM_PROGRAM"] = "vscode"
				environment["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"] = "1"
			}
		}
		if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"futureUser":7}`), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := NewSession(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		before, err := s.process.TerminalConfigState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, sourceCase{ID: pair.id, Value: pair.value, Global: obj(before.Get("global")), User: obj(before.Get("userSettings")), Local: obj(before.Get("localSettings")), Env: o.Environment, Supported: string(before.Get("isSupportedTerminal")) == "true"})
		key := pair.id
		if key == "workflows" {
			key = "enableWorkflows"
		}
		if key == "notifChannel" {
			key = "preferredNotifChannel"
		}
		if err = s.process.SetTerminalConfigSetting(context.Background(), key, raw(pair.value)); err != nil {
			t.Fatal(key, err)
		}
		after, err := s.process.TerminalConfigState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close(context.Background()) })
		results = append(results, after)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-config.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_CONFIG_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	input := raw(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Original callbacks %v %.5000s", err, output)
	}
	var source struct {
		Results []struct {
			ID                  string
			Global, User, Local protocol.JSONObject
			ThinkingEnabled     bool
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, want := range source.Results {
		for _, pair := range []struct {
			key  string
			want protocol.JSONObject
		}{{"global", want.Global}, {"userSettings", want.User}, {"localSettings", want.Local}} {
			var actual, expected any
			_ = json.Unmarshal(results[i].Get(pair.key), &actual)
			_ = json.Unmarshal(raw(pair.want), &expected)
			if !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s %s Go=%s Source=%s", want.ID, pair.key, results[i].Get(pair.key), raw(pair.want))
			}
		}
		if (string(results[i].Get("thinkingEnabled")) == "true") != want.ThinkingEnabled {
			t.Error(want.ID, "thinking consumer differs")
		}
	}
}
