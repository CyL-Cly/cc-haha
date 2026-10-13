package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePermissionStartupMatchesActualSourceModesPathsAndProvenance(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	rows := []map[string]any{}
	for _, mode := range []string{"default", "acceptEdits", "bypassPermissions"} {
		for _, enabled := range []bool{false, true} {
			rows = append(rows, map[string]any{"mode": mode, "settings": map[string]any{"skipAutoPermissionPrompt": enabled}})
		}
	}
	for _, mode := range []string{"default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto"} {
		rows = append(rows, map[string]any{"settings": map[string]any{"permissions": map[string]any{"defaultMode": mode}}})
	}
	rows = append(rows, map[string]any{}, map[string]any{"mode": "default", "settings": map[string]any{"permissions": map[string]any{"defaultMode": "plan"}}}, map[string]any{"mode": "acceptEdits", "skip": true}, map[string]any{"allow": true}, map[string]any{"mode": "invalid"})
	for _, request := range []map[string]any{{"skip": true}, {"mode": "bypassPermissions"}, {"skip": true, "mode": "plan"}, {"allow": true}} {
		request["settings"] = map[string]any{"permissions": map[string]any{"disableBypassPermissionsMode": "disable", "defaultMode": "acceptEdits"}}
		rows = append(rows, request)
	}
	for _, mode := range []string{"bypassPermissions", "dontAsk", "auto", "plan", "acceptEdits"} {
		rows = append(rows, map[string]any{"env": map[string]string{"CLAUDE_CODE_REMOTE": "yes"}, "settings": map[string]any{"permissions": map[string]any{"defaultMode": mode}}})
	}
	rows = append(rows, map[string]any{"skip": true, "env": map[string]string{"USER_TYPE": "ant", "CLAUDE_INTERNAL_FC_OVERRIDES": `{"tengu_disable_bypass_permissions_mode":true}`}}, map[string]any{"skip": true, "env": map[string]string{"USER_TYPE": "ant", "CLAUDE_INTERNAL_FC_OVERRIDES": `{"tengu_disable_bypass_permissions_mode":false}`}, "configOverrides": map[string]any{"tengu_disable_bypass_permissions_mode": true}}, map[string]any{"skip": true, "env": map[string]string{"USER_TYPE": "ant"}, "configOverrides": map[string]any{"tengu_disable_bypass_permissions_mode": "yes"}}, map[string]any{"skip": true, "env": map[string]string{"USER_TYPE": "external"}, "configOverrides": map[string]any{"tengu_disable_bypass_permissions_mode": true}})
	for _, enabled := range []any{"disabled", "enabled", "opt-in", "invalid", nil} {
		config := map[string]any{"tengu_auto_mode_config": map[string]any{"enabled": enabled}}
		encoded, _ := json.Marshal(config)
		for _, user := range []string{"ant", "external"} {
			rows = append(rows, map[string]any{"mode": "auto", "env": map[string]string{"USER_TYPE": user, "CLAUDE_INTERNAL_FC_OVERRIDES": string(encoded)}})
			rows = append(rows, map[string]any{"mode": "auto", "env": map[string]string{"USER_TYPE": user}, "configOverrides": config})
		}
	}
	paths := []string{"~/outside", "../outside/nested/", filepath.Join(home, "outside"), "", " ", "../missing", "../plain-file", filepath.Join(home, "project"), "../project/sub", "../outside/nested"}
	rows = append(rows, map[string]any{"settings": map[string]any{"permissions": map[string]any{"additionalDirectories": paths}}, "addDirs": []string{filepath.Join(home, "second"), filepath.Join(home, "second/sub")}}, map[string]any{"env": map[string]string{"PWD": filepath.Join(home, "project-alias")}, "addDirs": []string{filepath.Join(home, "outside"), filepath.Join(home, "project-alias/sub")}}, map[string]any{"env": map[string]string{"PWD": filepath.Join(home, "outside")}}, map[string]any{"settings": map[string]any{"permissions": map[string]any{"disableAutoMode": "disable"}}})
	fixture, _ := filepath.Abs("testdata/dontask-initializer-source.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PERMISSION_STARTUP_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "FORCE_COLOR=0", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows, "directories": []string{"outside/nested", "second/sub", "project/sub"}, "files": []string{"plain-file"}})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source startup %v %.4000s", e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Mode                           struct{ Mode, Notification string }
			Directories                    [][]json.RawMessage
			InstructionDirectories         []string
			Warnings                       []string
			BypassAvailable, AutoAvailable bool
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("actual Source startup result %v rows%d/%d blocked%v %.400s", e, len(source.Rows), len(rows), source.Capabilities.Blocked, output)
	}
	for i, row := range rows {
		owner := cooperative.New()
		t.Cleanup(owner.Close)
		var settings protocol.JSONObject
		raw, _ := json.Marshal(row["settings"])
		if row["settings"] == nil {
			raw = []byte(`{}`)
		}
		_ = settings.UnmarshalJSON(raw)
		env, _ := row["env"].(map[string]string)
		global := protocol.NewJSONObject()
		if value := row["configOverrides"]; value != nil {
			global.Set("growthBookOverrides", persistRaw(value))
		}
		producer, e := NewNativeStartup(owner, NativeStartupOptions{Home: home, CWD: filepath.Join(home, "project"), OriginalCWD: filepath.Join(home, "project"), Platform: "darwin", Environment: func(key string) string { return env[key] }, Global: func() (*protocol.JSONObject, error) { return &global, nil }, Stat: os.Stat, Lstat: os.Lstat, Realpath: filepath.EvalSymlinks})
		if e != nil {
			t.Fatal(e)
		}
		mode, _ := row["mode"].(string)
		skip, _ := row["skip"].(bool)
		allow, _ := row["allow"].(bool)
		dirs, _ := row["addDirs"].([]string)
		var actual NativeStartupResult
		e = owner.Run(ctx, func(scope *cooperative.Scope) error {
			var e error
			actual, e = producer.Prepare(scope, &settings, NativeStartupRequest{Mode: mode, DangerouslySkipPermissions: skip, AllowDangerouslySkipPermissions: allow, AdditionalDirectories: dirs})
			return e
		})
		if e != nil {
			t.Errorf("case%d %v", i, e)
			continue
		}
		if actual.Mode != source.Rows[i].Mode.Mode || actual.Notification != source.Rows[i].Mode.Notification || actual.BypassAvailable != source.Rows[i].BypassAvailable || actual.AutoAvailable != source.Rows[i].AutoAvailable || string(persistRaw(actual.Warnings)) != string(persistRaw(source.Rows[i].Warnings)) || string(persistRaw(actual.InstructionDirectories)) != string(persistRaw(source.Rows[i].InstructionDirectories)) {
			t.Errorf("case%d Go%+v Source%+v", i, actual, source.Rows[i])
		}
		var mapValue protocol.JSONObject
		_ = mapValue.UnmarshalJSON(actual.WorkingDirectories)
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(mapValue.Get("map")), protocol.StringifyValidatedJavaScriptJSON(persistRaw(source.Rows[i].Directories))) {
			t.Errorf("case%d directories Go%s Source%s", i, mapValue.Get("map"), persistRaw(source.Rows[i].Directories))
		}
	}
}
