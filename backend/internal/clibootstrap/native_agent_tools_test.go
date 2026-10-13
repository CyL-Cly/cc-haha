package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeCustomAgentAvailableToolsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	f := newNativePluginFixture(t)
	_ = os.MkdirAll(f.n.Temp, 0700)
	cases := []struct {
		Env         map[string]string `json:"env"`
		Settings    map[string]any    `json:"settings"`
		Platform    string            `json:"platform,omitempty"`
		GitBashPath string            `json:"gitBashPath,omitempty"`
	}{
		{}, {Env: map[string]string{"ANTHROPIC_MODEL": "deepseek-flash", "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}},
		{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://API.ANTHROPIC.COM:443"}},
		{Env: map[string]string{"ENABLE_TOOL_SEARCH": "auto:\uFEFF100"}},
		{Env: map[string]string{"ANTHROPIC_MODEL": "deepseek-flash", "ENABLE_TOOL_SEARCH": "true"}},
		{Env: map[string]string{"ANTHROPIC_MODEL": "sonnet", "ENABLE_TOOL_SEARCH": "false"}},
		{Env: map[string]string{"CLAUDE_CODE_ENABLE_TASKS": " yes ", "ENABLE_TOOL_SEARCH": "auto:100"}},
		{Env: map[string]string{"ENABLE_TOOL_SEARCH": "auto:0", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "on"}},
		{Env: map[string]string{"ENABLE_TOOL_SEARCH": "auto:99suffix"}},
		{Env: map[string]string{"ENABLE_TOOL_SEARCH": "auto:-12"}},
		{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com:8443"}},
		{Env: map[string]string{"EMBEDDED_SEARCH_TOOLS": "1", "CLAUDE_CODE_ENTRYPOINT": "cli", "CLAUDE_CODE_SIMPLE": "1"}},
		{Env: map[string]string{"EMBEDDED_SEARCH_TOOLS": "1", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli"}},
		{Settings: map[string]any{"model": "deepseek-flash", "webSearch": map[string]any{"mode": "auto", "tavilyApiKey": " fake-fixture "}}},
		{Settings: map[string]any{"webSearch": map[string]any{"mode": "disabled"}}},
		{Settings: map[string]any{"webSearch": map[string]any{"mode": "tavily"}}},
		{Settings: map[string]any{"model": "deepseek-flash", "webSearch": map[string]any{"mode": "anthropic", "braveApiKey": "fixture"}}},
		{Settings: map[string]any{"model": "deepseek-flash", "webSearch": map[string]any{"mode": "invalid", "braveApiKey": "fixture"}}},
		{Env: map[string]string{"ANTHROPIC_MODEL": "deepseek-flash"}, Settings: map[string]any{"model": "sonnet", "availableModels": []string{"sonnet"}}},
		{Platform: "windows"},
		{Platform: "windows", GitBashPath: `C:\Program Files\Git\bin\bash.exe`},
		{Platform: "windows", GitBashPath: `C:\Program Files\Git\bin\bash.exe`, Env: map[string]string{"CLAUDE_CODE_USE_POWERSHELL_TOOL": "true"}},
		{Platform: "windows", Env: map[string]string{"CLAUDE_CODE_USE_POWERSHELL_TOOL": "false"}},
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-tools.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_AGENT_TOOLS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=fake-agent-tools-fixture", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": f.home, "cases": cases})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source tools %v %.3000s", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Model    string
			Tools    []string
			Settings protocol.JSONObject
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped %v rows%d output%.120s", source.Capabilities.Blocked, len(source.Rows), output)
	}
	for i, row := range cases {
		f.n.Environment = map[string]string{"HOME": f.home, "USERPROFILE": f.home, "CLAUDE_CONFIG_DIR": f.config, "TMPDIR": f.n.Temp, "PATH": "/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "ANTHROPIC_API_KEY": "fake-agent-tools-fixture"}
		for k, v := range row.Env {
			f.n.Environment[k] = v
		}
		settingsData, _ := json.Marshal(row.Settings)
		if row.Settings == nil {
			settingsData = []byte(`{}`)
		}
		f.write(t, filepath.Join(f.config, "settings.json"), string(settingsData))
		model, _, err := resolveNativeModel(&source.Rows[i].Settings, f.n.Environment, "")
		if err != nil || model != source.Rows[i].Model {
			t.Fatalf("Source model case%d %s %v want%s", i, model, err, source.Rows[i].Model)
		}
		platform := "darwin"
		var shells []windowshell.Availability
		if row.Platform == "windows" {
			platform = "windows"
			setting, defined := row.Env["CLAUDE_CODE_USE_POWERSHELL_TOOL"]
			shells = append(shells, windowshell.ResolveAvailability(platform, row.GitBashPath, row.Env["USER_TYPE"], setting, defined))
		}
		names, err := nativeCustomAgentToolNames(platform, f.n.Environment, &source.Rows[i].Settings, model, shells...)
		if err != nil || !reflect.DeepEqual(names, source.Rows[i].Tools) {
			t.Errorf("case%d Go %v %v Source %v", i, names, err, source.Rows[i].Tools)
		}
		if row.Env["ANTHROPIC_BASE_URL"] == "" || strings.Contains(strings.ToLower(row.Env["ANTHROPIC_BASE_URL"]), "api.anthropic.com") || row.Settings != nil && row.Settings["webSearch"] != nil {
			continue
		} // Shared settings field remains explicitly guarded until runtime owner migration.
		producer, err := NewNativeProcessSettings(f.n, "darwin", DefaultState())
		if err != nil {
			t.Fatal(err)
		}
		integrated, err := producer.CustomAgentTools(context.Background())
		_ = producer.Close(context.Background())
		if err != nil || !reflect.DeepEqual(integrated, source.Rows[i].Tools) {
			t.Errorf("real producer case%d %v %v want%v", i, integrated, err, source.Rows[i].Tools)
		}

	}
}
