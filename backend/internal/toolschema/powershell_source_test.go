package toolschema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourcePowerShellSchemaParserPromptEditionsAndStartupBackgroundFlag(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	parsers := []json.RawMessage{[]byte(`{"command":"Write-Output 'literal'"}`), []byte(`{"command":"","timeout":"300.5","run_in_background":"true","dangerouslyDisableSandbox":" false "}`), []byte(`{"command":"Get-Location","description":"","timeout":-1,"run_in_background":false,"dangerouslyDisableSandbox":true}`), []byte(`{"command":"Read","timeout":null}`), []byte(`{"command":"Read","run_in_background":null}`), []byte(`{"command":1}`), []byte(`{"timeout":100}`), []byte(`{"command":"Read","unknown":"reject"}`), []byte(`{"command":"Read","timeout":"bad"}`)}
	for _, profile := range []struct {
		name, edition string
		env           map[string]string
	}{{"core-default", "core", nil}, {"desktop-default", "desktop", nil}, {"unknown-default", "unknown", nil}, {"startup-background-disabled", "core", map[string]string{"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": " true "}}, {"parse-int-timeout-output", "desktop", map[string]string{"BASH_DEFAULT_TIMEOUT_MS": " +42000suffix", "BASH_MAX_TIMEOUT_MS": "17", "BASH_MAX_OUTPUT_LENGTH": "160000bytes"}}} {
		t.Run(profile.name, func(t *testing.T) {
			home := t.TempDir()
			if resolved, err := filepath.EvalSymlinks(home); err == nil {
				home = resolved
			}
			config, temp := filepath.Join(home, ".claude"), filepath.Join(home, "tmp")
			for _, directory := range []string{config, temp, filepath.Join(home, "bin")} {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			name := "pwsh"
			if profile.edition == "desktop" {
				name = "powershell"
			}
			path := filepath.Join(home, "bin", name)
			if profile.edition != "unknown" {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				path = filepath.Join(home, "bin", "missing.exe")
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-shell.fixture.ts")
			sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WINDOWS_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-windows-schema", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_POWERSHELL_PATH=" + path, "PATH=/usr/bin:/bin"}
			for name, value := range profile.env {
				command.Env = append(command.Env, name+"="+value)
			}
			input, _ := json.Marshal(map[string]any{"home": home, "parsers": parsers})
			command.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatal("actual Source PowerShell definition", err, stderr.String())
			}
			var source struct {
				Schema  protocol.JSONObject
				Parsers []protocol.JSONObject
			}
			if err = json.Unmarshal(output, &source); err != nil || len(source.Parsers) != len(parsers) {
				t.Fatal(err, string(output))
			}
			def := PowerShellDefinition(profile.env)
			edition := profile.edition
			if edition == "unknown" {
				edition = ""
			}
			schemas, err := NewSession().RenderBatch([]Definition{def}, Options{Host: Host{BashEnvironment: profile.env, PowerShellEdition: edition}, Model: "deepseek-flash"})
			if err != nil || len(schemas) != 1 {
				t.Fatal(err)
			}
			got, _ := schemas[0].MarshalJSON()
			expected, _ := source.Schema.MarshalJSON()
			var left, right any
			_ = json.Unmarshal(got, &left)
			_ = json.Unmarshal(expected, &right)
			if !reflect.DeepEqual(left, right) {
				t.Errorf("PowerShell complete API schema or exact prompt differs\nGo%s\nSource%s", got, expected)
			}
			for index, input := range parsers {
				var value protocol.JSONObject
				_ = value.UnmarshalJSON(input)
				parsed, err := ParseInput(def, value)
				row := protocol.NewJSONObject()
				if err == nil {
					data, _ := parsed.MarshalJSON()
					row.Set("data", data)
				} else {
					var validation *ValidationError
					if !errors.As(err, &validation) {
						t.Fatal("unexpected parser capability", err)
					}
					row.Set("issues", validation.Issues)
				}
				got, _ := row.MarshalJSON()
				expected, _ := source.Parsers[index].MarshalJSON()
				_ = json.Unmarshal(got, &left)
				_ = json.Unmarshal(expected, &right)
				if !reflect.DeepEqual(left, right) {
					t.Errorf("original semantic/strict parser row%d Go%s Source%s", index, got, expected)
				}
			}
		})
	}
}
