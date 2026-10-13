package ideintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestIDERunningDetectionAndPluginDirectoriesAgainstActualSourcePhysicalFS(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	cases := []map[string]any{}
	for _, platform := range []string{"macos", "windows", "linux", "unknown"} {
		for _, text := range []string{"", "Cursor Helper Code Helper PyCharm GoLand Fleet Aqua", "CODE.EXE Cursor.EXE studio64.exe aqua64.exe gateway64.exe fleet.exe", "pycharm code cursor appcode idea android-studio", "visual studio code", "code"} {
			cases = append(cases, map[string]any{"mode": "running", "platform": platform, "stdout": text})
		}
	}
	cases = append(cases, map[string]any{"mode": "running", "platform": "macos", "throw": true})
	for _, platform := range []string{"darwin", "win32", "linux", "unknown"} {
		for _, kind := range []string{"pycharm", "intellij", "androidstudio", "unknown"} {
			cases = append(cases, map[string]any{"mode": "plugins", "platform": platform, "kind": kind, "pathsOnly": true, "environment": map[string]string{"APPDATA": "C:\\custom\\Roaming", "LOCALAPPDATA": "C:\\custom\\Local"}})
		}
	}
	for _, profile := range []struct{ platform, kind, path string }{{"darwin", "pycharm", "Library/Application Support/JetBrains/PyCharm2026.1/plugins/claude-code-jetbrains-plugin"}, {"linux", "intellij", ".local/share/JetBrains/IdeaIC2026.1/claude-code-jetbrains-plugin"}, {"darwin", "androidstudio", "Library/Application Support/Google/AndroidStudio2026.1/plugins/claude-code-jetbrains-plugin"}, {"linux", "unknown", ".config/JetBrains/Unknown/claude-code-jetbrains-plugin"}} {
		if e := os.MkdirAll(filepath.Join(home, profile.path), 0700); e != nil {
			t.Fatal(e)
		}
		cases = append(cases, map[string]any{"mode": "plugins", "platform": profile.platform, "kind": profile.kind})
	}
	if runtime.GOOS != "windows" {
		real := filepath.Join(home, "Library", "Application Support", "JetBrains", "PyCharm2026.1")
		alias := filepath.Join(filepath.Dir(real), "PyCharmAlias")
		if e := os.Symlink(real, alias); e != nil {
			t.Fatal(e)
		}
	}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "cases": cases})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-ide-runtime.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var expected []struct {
		IDEs        []string
		Calls       []json.RawMessage
		Directories []string
		Installed   bool
	}
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range cases {
		platform := row["platform"].(string)
		if platform == "win32" {
			platform = "windows"
		}
		env := map[string]string{}
		if value, ok := row["environment"].(map[string]string); ok {
			env = value
		}
		calls := []string{}
		service := NewExtensions(ExtensionOptions{Home: home, Platform: platform, Environment: env, Stat: os.Stat, ReadDir: os.ReadDir, RunShell: func(_ context.Context, command string) (ExtensionProcessResult, error) {
			calls = append(calls, command)
			if row["throw"] == true {
				return ExtensionProcessResult{}, errors.New("fixture process failure")
			}
			return ExtensionProcessResult{Stdout: row["stdout"].(string)}, nil
		}})
		if row["mode"] == "running" {
			actual := service.Running(ctx)
			if !reflect.DeepEqual(actual, expected[i].IDEs) || len(calls) != len(expected[i].Calls) {
				t.Fatalf("row%d running Source%#v actual%#v calls%#v", i, expected[i], actual, calls)
			}
			if len(calls) > 0 {
				var call []json.RawMessage
				_ = json.Unmarshal(expected[i].Calls[0], &call)
				var sourceCommand string
				_ = json.Unmarshal(call[0], &sourceCommand)
				if calls[0] != sourceCommand {
					t.Fatal("actual Source shell command differs", calls[0], sourceCommand)
				}
			}
		} else {
			kind := row["kind"].(string)
			directories := service.PluginDirectories(kind)
			if !reflect.DeepEqual(directories, expected[i].Directories) {
				t.Fatalf("row%d directories Source%#v actual%#v", i, expected[i].Directories, directories)
			}
			if row["pathsOnly"] != true && service.Installed(ctx, kind) != expected[i].Installed {
				t.Fatal("actual private JetBrains plugin detection differs", i, expected[i])
			}
		}
	}
}
