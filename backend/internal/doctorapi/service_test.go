package doctorapi

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)

func fixtureSource(t *testing.T, input any) []json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-diagnostics.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = home
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_DESKTOP_DIAGNOSTICS_FIXTURE=1"}
	b, _ := json.Marshal(input)
	command.Stdin = bytes.NewReader(b)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("Source Doctor %v %s", err, stderr.String())
	}
	var result []json.RawMessage
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatalf("Source Doctor output %s %v", out, err)
	}
	return result
}
func decode(value any) any {
	b, _ := json.Marshal(value)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}
func seed(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestDoctorReportAndProtectedRepairAgainstActualSource(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	project := filepath.Join(home, "project")
	files := map[string]string{"settings.json": "{}", "cc-haha/providers.json": `{"activeId":null,"providers":[]}`, "cc-haha/settings.json": "", "adapters.json": "null", "adapter-sessions.json": "[]", ".claude.json": "{broken", "projects/test/session.jsonl": "{}\n{broken\n\nnull\r\n", "skills/.DS_Store": "ignore", "skills/skill/SKILL.md": "# Test", "plugins/installed.json": "{}"}
	projectFiles := map[string]string{".claude/settings.json": "{", ".mcp.json": "{}", ".agents/skills/example/SKILL.md": "# Test"}
	for path, data := range files {
		seed(t, filepath.Join(config, filepath.FromSlash(path)), data)
	}
	for path, data := range projectFiles {
		seed(t, filepath.Join(project, filepath.FromSlash(path)), data)
	}
	service, err := New(Options{HomeDir: home, ConfigDir: config, ProjectRoot: project, UsesConfigDirOverride: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.GetReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	repair, err := service.Repair(context.Background(), []string{"user-settings", "user-mcp", "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	service.options.AgentSkillsEnabled = func(context.Context) bool { return false }
	disabled, err := service.GetReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source := fixtureSource(t, map[string]any{"now": now.Format(time.RFC3339Nano), "files": files, "projectFiles": projectFiles, "cases": []any{map[string]any{"action": "doctor", "project": true}, map[string]any{"action": "doctorRepair", "project": true, "targetIds": []string{"user-settings", "user-mcp", "unknown"}}, map[string]any{"action": "doctor", "project": true, "agents": false}}})
	for i, got := range []any{report, repair, disabled} {
		if !reflect.DeepEqual(decode(got), decode(source[i])) {
			g, _ := json.MarshalIndent(got, "", "  ")
			t.Fatalf("case %d\nGo %s\nSource %s", i, g, source[i])
		}
	}
	for path, data := range files {
		actual, err := os.ReadFile(filepath.Join(config, filepath.FromSlash(path)))
		if err != nil || string(actual) != data {
			t.Fatalf("protected file mutated %s %v", path, err)
		}
	}
}
func TestDoctorProviderSchemaAgainstActualSource(t *testing.T) {
	provider := map[string]any{"id": "provider", "presetId": "custom", "name": "Example", "apiKey": "fake", "baseUrl": "https://example.com", "models": map[string]any{"main": "flash", "haiku": "flash", "sonnet": "flash", "opus": "flash"}}
	valid := func() map[string]any {
		b, _ := json.Marshal(provider)
		var p any
		_ = json.Unmarshal(b, &p)
		return map[string]any{"activeId": nil, "providers": []any{p}}
	}
	values := []any{nil, []any{}, map[string]any{}, map[string]any{"activeId": 15, "providers": nil}, valid()}
	for _, row := range []struct {
		key   string
		value any
	}{{"name", ""}, {"apiFormat", "bad"}, {"authStrategy", nil}, {"runtimeKind", 17}, {"autoCompactWindow", 12}, {"autoCompactWindow", 16000.5}, {"toolSearchEnabled", "false"}, {"model1mSupport", map[string]any{"main": false}}, {"requestCompatibility", map[string]any{"maxOutputTokens": 0, "parallelTools": "bad"}}, {"imageGeneration", map[string]any{"model": " "}}, {"modelContextWindows", map[string]any{"deepseek": 15000}}} {
		v := valid()
		v["providers"].([]any)[0].(map[string]any)[row.key] = row.value
		values = append(values, v)
	}
	cases := []any{}
	for _, v := range values {
		cases = append(cases, map[string]any{"action": "providerSchema", "value": v})
	}
	source := fixtureSource(t, map[string]any{"now": now.Format(time.RFC3339Nano), "cases": cases})
	for i, v := range values {
		raw, _ := json.Marshal(v)
		got := validateProviders(raw)
		var want string
		_ = json.Unmarshal(source[i], &want)
		if got != want {
			t.Fatalf("schema case %d\nGo %s\nSource %s", i, got, want)
		}
	}
}
func TestDoctorDefaultMCPPathAliasesAndNoMutation(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	seed(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{}}`)
	s, err := New(Options{HomeDir: home, ConfigDir: config})
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.GetReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range report.Items {
		if item.ID == "user-mcp" {
			found = true
			if item.Path != "~/.claude.json" || item.Status != "ok" {
				t.Fatal(item)
			}
		}
	}
	if !found {
		t.Fatal("no user MCP check")
	}
	repair, err := s.Repair(context.Background(), nil)
	if err != nil || repair.Mutated || !repair.DryRun || len(repair.Operations) != 0 || len(repair.Skips) != len(report.Items) {
		t.Fatalf("unsafe repair %+v %v", repair, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude.json")); !strings.Contains(string(b), "mcpServers") {
		t.Fatal("protected config modified")
	}
}
