package clibootstrap

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

func TestTerminalConfigAutoUpdaterDisabledReasonActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun required")
	}
	rows := []struct {
		Env    map[string]string   `json:"env"`
		Global protocol.JSONObject `json:"global"`
	}{}
	for _, env := range []map[string]string{{}, {"NODE_ENV": "development"}, {"DISABLE_AUTOUPDATER": "\ufeff TRUE\u00a0"}, {"DISABLE_AUTOUPDATER": "false"}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "false"}} {
		for _, global := range []string{`{}`, `{"autoUpdates":false}`, `{"autoUpdates":false,"installMethod":"native","autoUpdatesProtectedForNative":true}`} {
			rows = append(rows, struct {
				Env    map[string]string   `json:"env"`
				Global protocol.JSONObject `json:"global"`
			}{env, mainObj([]byte(global))})
		}
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-config-channel.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_CONFIG_CHANNEL_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source %v %.7000s", err, output)
	}
	var source struct{ Reasons []*protocol.JSONObject }
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		actual := terminalAutoUpdaterDisabledReason(row.Env, &row.Global)
		var a, b any
		_ = json.Unmarshal(rawMain(actual), &a)
		_ = json.Unmarshal(rawMain(source.Reasons[i]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatal(i, string(rawMain(actual)), string(rawMain(source.Reasons[i])))
		}
	}
}
func TestTerminalUpdaterOriginalRunningRefInitialEffectAndThirtyMinuteInterval(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	rows := []map[string]any{{"disabled": true}, {"running": true}, {"result": map[string]any{"latestVersion": "2.3.5", "wasUpdated": true}}, {"result": map[string]any{"latestVersion": "2.3.5", "wasUpdated": false}}, {"result": map[string]any{"lockFailed": true}}, {"error": true}, {"hold": true, "result": map[string]any{"latestVersion": "2.3.5", "wasUpdated": true}}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-updater.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_UPDATER_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Original updater callback %v %.6000s", err, output)
	}
	var source struct {
		IntervalMs int
		Results    []struct {
			Installs int
			Events   []protocol.JSONObject
			Running  bool
		}
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if source.IntervalMs != 30*60*1000 || len(source.Results) != 7 {
		t.Fatal(string(output))
	}
	for i, result := range source.Results {
		expected := 1
		if i < 2 {
			expected = 0
		}
		if result.Installs != expected || i != 1 && result.Running || i == 1 && !result.Running {
			t.Fatal(i, string(output))
		}
	}
	if len(source.Results[3].Events) != 3 || len(source.Results[4].Events) != 2 || mainObj(source.Results[2].Events[2].Get("result")).String("status") != "success" || mainObj(source.Results[5].Events[1].Get("result")).String("status") != "install_failed" {
		t.Fatal(string(output))
	}
}
