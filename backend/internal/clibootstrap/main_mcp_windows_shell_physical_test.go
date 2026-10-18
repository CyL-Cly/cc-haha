package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This is the normal MCP constructor/caller with an explicit Windows contract,
// disposable native Darwin IO and the installed pwsh. It proves the production
// factory does not stop at a total Windows capability guard. Native Windows
// process creation/TTY is a separate platform acceptance lane.
func TestWindowsMCPShellFactoryRunsActualPowerShellAndEnforcesMandatoryPolicy(t *testing.T) {
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("actual PowerShell required")
	}
	for _, bare := range []bool{true, false} {
		name := "ordinary"
		if bare {
			name = "bare"
		}
		t.Run(name, func(t *testing.T) {
			for _, mandatory := range []bool{false, true} {
				label := "allowed"
				if mandatory {
					label = "mandatory-sandbox"
				}
				t.Run(label, func(t *testing.T) {
					f := newNativePluginFixture(t)
					settings := map[string]any{"enabledPlugins": map[string]bool{"sample@mkt": false}}
					if mandatory {
						settings["sandbox"] = map[string]any{"enabled": true, "allowUnsandboxedCommands": false}
					}
					f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
					f.n.Environment["CLAUDE_CODE_POWERSHELL_PATH"] = executable
					f.n.Environment["ANTHROPIC_API_KEY"] = "offline-windows-mcp"
					f.n.Environment["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:1"
					f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
					f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
					f.n.Environment["PATH"] = "/usr/bin:/bin"
					f.n.Environment["TMPDIR"] = f.n.Temp
					f.n.Environment["COMSPEC"] = "OWNED_ORIGINAL_COMSPEC"
					f.n.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
					args := []string{"mcp", "serve"}
					if bare {
						args = append([]string{"--bare"}, args...)
					}
					// The command's OS bindings remain the actual host. The MCP
					// factory below is the Windows consumer being verified.
					command, err := NewMainCommand(MainCommandOptions{Arguments: args, Native: f.n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), NonInteractive: true, Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
					if err != nil {
						t.Fatal(err)
					}
					defer command.Close()
					host, err := NewNativeMCPToolContext(command, "windows", false, false)
					if err != nil {
						t.Fatal("production Windows MCP factory", err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					defer host.Close(context.Background())
					catalogue, err := host.Catalog(ctx)
					if err != nil {
						t.Fatal(err)
					}
					ps, bash := 0, 0
					for _, tool := range catalogue {
						switch tool.String("name") {
						case "PowerShell":
							ps++
							if !strings.Contains(tool.String("description"), "PowerShell edition: PowerShell 7+ (pwsh)") {
								t.Fatal("actual discovered edition did not reach MCP API", string(rawMain(tool)))
							}
						case "Bash":
							bash++
						}
					}
					if ps != 1 || bash != 0 || f.n.Environment["COMSPEC"] != "OWNED_ORIGINAL_COMSPEC" {
						t.Fatal("Source no-Git shell availability or COMSPEC changed", ps, bash)
					}
					marker := filepath.Join(f.cwd, "mcp-powershell.txt")
					result, err := host.Call(ctx, "PowerShell", rawMain(map[string]any{"command": "Set-Content -LiteralPath '" + marker + "' -NoNewline -Value 'ACTUAL_WINDOWS_MCP_PS'; Write-Output 'ACTUAL_WINDOWS_MCP_PS'"}))
					if err != nil {
						t.Fatal(err)
					}
					content := taskBlocks(result.Get("content"))
					if mandatory {
						if string(result.Get("isError")) != "true" || len(content) != 1 || !strings.Contains(content[0].String("text"), builtintools.WindowsPowerShellSandboxRefusal) {
							t.Fatal("native Windows mandatory policy was bypassed", string(rawMain(result)))
						}
						if _, err := os.Stat(marker); !os.IsNotExist(err) {
							t.Fatal("blocked PowerShell created actual file", err)
						}
					} else {
						if len(content) != 1 || string(result.Get("isError")) == "true" {
							t.Fatal("actual PowerShell MCP call failed", string(rawMain(result)))
						}
						var data protocol.JSONObject
						if err = json.Unmarshal([]byte(content[0].String("text")), &data); err != nil || data.String("stdout") != "ACTUAL_WINDOWS_MCP_PS" {
							t.Fatal("MCP raw shell data consumer mismatch", err, string(rawMain(result)))
						}
						physical, err := os.ReadFile(marker)
						if err != nil || string(physical) != "ACTUAL_WINDOWS_MCP_PS" {
							t.Fatal("actual MCP process did not write private marker", string(physical), err)
						}
						subdir := filepath.Join(f.cwd, "mcp-ps-cwd")
						if err := os.MkdirAll(subdir, 0700); err != nil {
							t.Fatal(err)
						}
						// The physical interpreter reports canonical cwd, including
						// macOS's /var → /private/var namespace transition.
						subdir, err = filepath.EvalSymlinks(subdir)
						if err != nil {
							t.Fatal(err)
						}
						for index, command := range []string{"Set-Location -LiteralPath '" + subdir + "'; Write-Output 'ACTUAL_WINDOWS_MCP_CWD'", "(Get-Location).Path", "Write-Output 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jBuoAAAAASUVORK5CYII='"} {
							result, err := host.Call(ctx, "PowerShell", rawMain(map[string]any{"command": command}))
							if err != nil || string(result.Get("isError")) == "true" {
								t.Fatal("MCP retained cwd/image call failed", index, err, string(rawMain(result)))
							}
							content := taskBlocks(result.Get("content"))
							var data protocol.JSONObject
							if len(content) != 1 || data.UnmarshalJSON([]byte(content[0].String("text"))) != nil {
								t.Fatal("MCP raw data absent", string(rawMain(result)))
							}
							if index == 1 && data.String("stdout") != subdir || index == 2 && (string(data.Get("isImage")) != "true" || !strings.HasPrefix(data.String("stdout"), "data:image/png;base64,")) {
								t.Fatal("MCP cwd producer or image normalization was lost", index, string(rawMain(data)))
							}
						}
					}
					if err = host.Close(ctx); err != nil {
						t.Fatal("actual MCP native references did not join", err)
					}
				})
			}
		})
	}
}
