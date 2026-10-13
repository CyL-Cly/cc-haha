package ideintegration

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
)

func TestIDEAutoConnectGateMatchesActualSourceHookCallbackAndDynamicPrecedence(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	ide := map[string]any{"url": "ws://127.0.0.1:1234", "name": "Owned IDE", "authToken": "private-token", "ideRunningInWindows": false}
	rows := []map[string]any{}
	for _, flag := range []bool{false, true} {
		for _, global := range []bool{false, true} {
			for _, supported := range []bool{false, true} {
				for _, value := range []string{"", "true", "false", "  OFF  ", "on", "0"} {
					rows = append(rows, map[string]any{"ide": ide, "flag": flag, "global": map[string]bool{"autoConnectIde": global}, "supported": supported, "env": map[string]string{"CLAUDE_CODE_AUTO_CONNECT_IDE": value}})
				}
			}
		}
	}
	rows = append(rows, map[string]any{"ide": ide, "env": map[string]string{"CLAUDE_CODE_SSE_PORT": "1234"}}, map[string]any{"ide": ide, "flag": true, "previous": map[string]any{"ide": map[string]string{"type": "http", "url": "http://explicit.invalid"}, "future": 17}}, map[string]any{"ide": nil, "flag": true})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-ide-hook.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_IDE_HOOK_FIXTURE=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	command.Stdin = bytes.NewReader(data)
	out, e := command.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var actual struct {
		Rows []struct {
			Dynamic                any
			FindStarted, Installed int
		}
	}
	if e = json.Unmarshal(out, &actual); e != nil || len(actual.Rows) != len(rows) {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		env, _ := row["env"].(map[string]string)
		global, _ := row["global"].(map[string]bool)
		flag, _ := row["flag"].(bool)
		supported, _ := row["supported"].(bool)
		terminal := ""
		if supported {
			terminal = "vscode"
		}
		expected := row["previous"]
		if row["ide"] != nil && AutoConnect(flag, global["autoConnectIde"], terminal, env) && expected == nil {
			expected = map[string]any{"ide": map[string]any{"type": "ws-ide", "url": ide["url"], "ideName": ide["name"], "authToken": ide["authToken"], "ideRunningInWindows": false, "scope": "dynamic"}}
		}
		raw, _ := json.Marshal(expected)
		var normalized any
		_ = json.Unmarshal(raw, &normalized)
		if !reflect.DeepEqual(normalized, actual.Rows[i].Dynamic) || actual.Rows[i].FindStarted != 1 || actual.Rows[i].Installed != 0 {
			t.Fatalf("row%d Source%+v Go%+v", i, actual.Rows[i], normalized)
		}
	}
}
