package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPermissionPersistenceSixActionsMatchActualSourceFiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	initial := `{"future":{"keep":true},"permissions":{"allow":["Task(*)","Bash()","Read(/keep/**)"],"deny":["Write"],"additionalDirectories":["/keep"]}}`
	for _, source := range []string{"userSettings", "projectSettings", "localSettings"} {
		for _, kind := range []string{"addRules", "replaceRules", "removeRules", "addDirectories", "removeDirectories", "setMode"} {
			update := map[string]any{"type": kind, "destination": source}
			switch kind {
			case "addRules", "replaceRules", "removeRules":
				update["behavior"] = "allow"
				update["rules"] = []map[string]any{{"toolName": "Agent"}, {"toolName": "Bash"}, {"toolName": "Read", "ruleContent": "/new/(scope)\\x"}, {"toolName": "Read", "ruleContent": "/new/(scope)\\x"}}
			case "addDirectories", "removeDirectories":
				update["directories"] = []string{"/keep", "/new", "/new"}
			case "setMode":
				update["mode"] = "acceptEdits"
			}
			rows = append(rows, map[string]any{"files": []string{initial, initial, initial}, "updates": []map[string]any{update}})
		}
	}
	rows = append(rows, map[string]any{"files": []string{`{"hooks":123,"future":2,"permissions":{"allow":["Task(*)"]}}`, "{}", "{}"}, "updates": []map[string]any{{"type": "addRules", "destination": "userSettings", "behavior": "allow", "rules": []map[string]string{{"toolName": "Agent"}, {"toolName": "WebFetch", "ruleContent": "domain:example.org"}}}}})
	for _, initial := range []string{`{bad`, `null`, "  \n", `{"permissions":{"allow":[12,null,"Bash()","Task(*)"]},"future":true}`, `{"permissions":{"allow":["Read(\ud800)","\ud801\udc28tool","ßTool","Task(*)","mcp__server","mcp__server__tool"]},"future":true}`} {
		for _, kind := range []string{"addRules", "removeRules", "setMode"} {
			update := map[string]any{"type": kind, "destination": "userSettings", "behavior": "allow", "rules": []map[string]string{{"toolName": "WebFetch", "ruleContent": "domain:example.org"}}, "mode": "acceptEdits"}
			rows = append(rows, map[string]any{"files": []string{initial, "{}", "{}"}, "updates": []map[string]any{update}})
		}
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-permission-persistence.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PERMISSION_PERSISTENCE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source persistence %v %.3000s", e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Files []string
			Error string
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped%v %.2400s", source.Capabilities.Blocked, out)
	}
	for i, row := range rows {
		root := t.TempDir()
		config, cwd := filepath.Join(root, ".claude"), filepath.Join(root, "project")
		_ = os.MkdirAll(cwd, 0700)
		state, _ := clisettings.NewState(func() float64 { return 1700000000000 })
		settings, e := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: cwd, PID: 17, Now: func() float64 { return 1700000000000 }, Profile: clisettings.NativeProcessProfile})
		if e != nil {
			t.Fatal(e)
		}
		for j, dest := range []string{"userSettings", "projectSettings", "localSettings"} {
			p, e := settings.Path(dest, "")
			if e != nil {
				t.Fatal(e)
			}
			_ = os.MkdirAll(filepath.Dir(p), 0700)
			_ = os.WriteFile(p, []byte(row["files"].([]string)[j]), 0600)
		}
		persistence, e := NewPersistence(PersistenceOptions{ValidatedSource: func(source string) (*protocol.JSONObject, error) {
			return settings.ReadPermissionValidatedSource(source)
		}, EditingSource: settings.ReadPermissionEditingSource, ManagedRulesOnly: func() (bool, error) { return false, nil }, LogError: func(e error) { t.Logf("case%d writer %v", i, e) }, UpdateSource: func(source string, patch protocol.JSONObject) error {
			result, e := settings.UpdatePermissionEditingSource(source, patch, clisettings.PermissionEditingBindings{StartLocalIgnore: func(string, string) error { return nil }})
			if e != nil {
				return e
			}
			return result.Error
		}})
		if e != nil {
			t.Fatal(e)
		}
		var updates []protocol.JSONObject
		_ = json.Unmarshal(persistRaw(row["updates"]), &updates)
		for _, u := range updates {
			if e = persistence.Persist(u); e != nil {
				t.Fatal(e)
			}
		}
		for j, dest := range []string{"userSettings", "projectSettings", "localSettings"} {
			p, _ := settings.Path(dest, "")
			actual, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			if string(actual) != source.Rows[i].Files[j] {
				t.Errorf("case%d %s Go%s Source%s", i, dest, actual, source.Rows[i].Files[j])
			}
		}
	}
}
