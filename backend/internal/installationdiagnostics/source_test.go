package installationdiagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualPackageManagerAndFullDiagnosticSourceConsumers(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	if real, e := filepath.EvalSymlinks(home); e == nil {
		home = real
	}
	cases := []map[string]any{}
	for _, platform := range []string{"macos", "linux", "wsl", "windows"} {
		for _, path := range []string{"/opt/homebrew/Caskroom/claude/2/claude", "/opt/homebrew/caskroom/claude", "C:\\Users\\u\\Microsoft\\WinGet\\Packages\\go-cli\\claude.exe", "/u/mise/installs/claude/1/claude", "/u/.ASDF/installs/claude/1/claude", "/tmp/claude"} {
			cases = append(cases, map[string]any{"mode": "packages", "platform": platform, "executable": path})
		}
	}
	for _, release := range []string{"ID=arch\n", "ID=ubuntu\nID_LIKE='debian'\n", "ID=fedora\n", "ID=suse\n", "ID=alpine\n", "ID=other\nID_LIKE=\"arch  debian\"\n", "ID=Debian\n", "ID=\"debi'an\"\n", "ID=arch\u00a0\n", "ID_LIKE=debian\u2028\n", "ID=arch\nID_LIKE=\"debian\"\r\n", ""} {
		for _, command := range []string{"pacman", "dpkg", "rpm", "apk", "none"} {
			cases = append(cases, map[string]any{"mode": "packages", "platform": "linux", "executable": "/tmp/claude", "osRelease": release, "commands": map[string]any{command: map[string]any{"code": 0, "stdout": "owned ", "stderr": ""}}})
		}
	}
	for _, platform := range []string{"macos", "windows", "linux"} {
		base := map[string]any{"mode": "diagnostic", "platform": platform, "executable": "/private/fixture/claude", "version": "2.1.288", "global": map[string]any{"installMethod": "native", "unknown": map[string]any{"kept": true}}, "environment": map[string]string{"PATH": "~/.local/bin/", "SHELL": "/bin/zsh"}}
		cases = append(cases, base)
		for _, managed := range []any{true, false, nil, "agents", 42, map[string]any{}, []any{"skills", "future", "agents", 3, "later"}} {
			row := cloneCase(base)
			row["managed"] = map[string]any{"strictPluginOnlyCustomization": managed}
			cases = append(cases, row)
		}
		for _, env := range []map[string]string{{}, {"NODE_ENV": "development"}, {"DISABLE_AUTOUPDATER": "true", "DISABLE_INSTALLATION_CHECKS": "true"}, {"DISABLE_AUTOUPDATER": "\ufeff TRUE\u00a0", "DISABLE_INSTALLATION_CHECKS": " On "}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "false"}, {"PATH": "~/.local/bin\\", "SHELL": "/bin/fish"}} {
			row := cloneCase(base)
			row["environment"] = env
			row["global"] = map[string]any{"autoUpdates": false}
			row["patterns"] = []string{"/one/*", "/two/*", "/three/*", "/four/*"}
			cases = append(cases, row)
		}
	}
	for _, platform := range []string{"macos", "windows"} {
		join := func(parts ...string) string {
			options := New(Options{PackageOptions: PackageOptions{Platform: platform}})
			return options.join(parts...)
		}
		prefix := join(home, "npm")
		bin := join(prefix, "bin", "claude")
		if platform == "windows" {
			bin = join(prefix, "claude")
		}
		for _, profile := range []string{"global", "orphan", "local", "native-data", "same-brew"} {
			files := []string{}
			switch profile {
			case "global", "same-brew":
				files = append(files, bin)
			case "orphan":
				files = append(files, join(prefix, "lib", "node_modules", "@anthropic-ai/claude-code"), join(prefix, "lib", "node_modules", "@fixture/go-cli"))
				if platform == "windows" {
					files = []string{join(prefix, "node_modules", "@anthropic-ai/claude-code"), join(prefix, "node_modules", "@fixture/go-cli")}
				}
			case "local":
				files = append(files, join(home, ".claude", "local", "node_modules", ".bin", "claude"))
			case "native-data":
				files = append(files, join(home, ".local", "share", "claude"))
			}
			row := map[string]any{"mode": "diagnostic", "platform": platform, "executable": "/private/fixture/claude", "files": files, "commands": map[string]any{"npm": map[string]any{"code": 0, "stdout": prefix + "\n"}}, "global": map[string]any{"installMethod": "native"}, "environment": map[string]string{"PATH": "$HOME/.local/bin"}}
			if profile == "same-brew" {
				row["executable"] = "/opt/homebrew/Caskroom/claude/1/claude"
				row["realpaths"] = map[string]string{bin: "/opt/homebrew/Caskroom/claude/1/claude"}
			}
			cases = append(cases, row)
		}
	}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "cases": cases})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-installation-diagnostic.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	command.Stdin = bytes.NewReader(input)
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var expected []struct {
		Manager, Second string
		Release         *OSRelease
		Diagnostic      json.RawMessage
		Calls           []struct {
			Command string
			Args    []string
			Options struct {
				Timeout int
				UseCWD  *bool
			}
		}
	}
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatal(e, string(output))
	}
	info, _ := os.Stat(home)
	for index, row := range cases {
		options := Options{Home: home, CWD: home, ManagedRoot: home, Version: "2.1.288", PackageName: "@fixture/go-cli", Bundled: true, Environment: map[string]string{}, Global: protocol.NewJSONObject(), Ripgrep: RipgrepStatus{Working: true, Mode: "system", SystemPath: stringPointer("/private/fixture/rg")}}
		options.Platform, _ = row["platform"].(string)
		options.Executable, _ = row["executable"].(string)
		options.Argv0, _ = row["argv0"].(string)
		options.Invoked, _ = row["invoked"].(string)
		if env, ok := row["environment"].(map[string]string); ok {
			options.Environment = env
		}
		if g, ok := row["global"]; ok {
			raw, _ := json.Marshal(g)
			_ = options.Global.UnmarshalJSON(raw)
		}
		if patterns, ok := row["patterns"].([]string); ok {
			options.LinuxGlobPatterns = patterns
		}
		if bundled, ok := row["bundled"].(bool); ok {
			options.Bundled = bundled
		}
		options.ReadFile = func(path string) ([]byte, error) {
			if path == "/etc/os-release" {
				if release, ok := row["osRelease"].(string); ok {
					return []byte(release), nil
				}
			}
			if path == New(options).join(home, "managed-settings.json") {
				if managed, ok := row["managed"]; ok {
					return json.Marshal(managed)
				}
			}
			return nil, fs.ErrNotExist
		}
		options.Stat = func(path string) (fs.FileInfo, error) {
			if files, ok := row["files"].([]string); ok {
				for _, file := range files {
					if file == path {
						return info, nil
					}
				}
			}
			return nil, fs.ErrNotExist
		}
		options.Realpath = func(path string) (string, error) {
			if paths, ok := row["realpaths"].(map[string]string); ok {
				if result := paths[path]; result != "" {
					return result, nil
				}
			}
			return "", fs.ErrNotExist
		}
		calls := []struct {
			Command string
			Args    []string
			Options RunOptions
		}{}
		options.Run = func(_ context.Context, name string, args []string, opts RunOptions) (ProcessResult, error) {
			calls = append(calls, struct {
				Command string
				Args    []string
				Options RunOptions
			}{name, args, opts})
			if commands, ok := row["commands"].(map[string]any); ok {
				if value, ok := commands[name]; ok {
					raw, _ := json.Marshal(value)
					var result ProcessResult
					if json.Unmarshal(raw, &result) != nil {
						return ProcessResult{}, errors.New("fixture invalid result")
					}
					return result, nil
				}
			}
			return ProcessResult{Code: 1}, nil
		}
		service := New(options)
		if row["mode"] == "packages" {
			manager := service.packages.Detect(ctx)
			second := service.packages.Detect(ctx)
			release := service.packages.OSRelease()
			if manager != expected[index].Manager || second != expected[index].Second || !reflect.DeepEqual(release, expected[index].Release) {
				t.Fatalf("row%d %v Source manager=%s release=%#v Go manager=%s release=%#v", index, row, expected[index].Manager, expected[index].Release, manager, release)
			}
		} else {
			raw, _ := json.Marshal(service.Diagnose(ctx))
			var got, want any
			_ = json.Unmarshal(raw, &got)
			_ = json.Unmarshal(expected[index].Diagnostic, &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("row%d %#v\nSource=%s\nGo=%s", index, row, expected[index].Diagnostic, raw)
			}
		}
		if len(calls) != len(expected[index].Calls) {
			t.Fatalf("row%d Source calls%#v Go calls%#v", index, expected[index].Calls, calls)
		}
		for i, call := range calls {
			source := expected[index].Calls[i]
			if call.Command != source.Command || !reflect.DeepEqual(call.Args, source.Args) || int(call.Options.Timeout/time.Millisecond) != source.Options.Timeout || source.Options.UseCWD != nil && call.Options.UseCWD != *source.Options.UseCWD {
				t.Fatalf("row%d call%d Source%#v Go%#v", index, i, source, call)
			}
		}
	}
}
func cloneCase(value map[string]any) map[string]any {
	out := map[string]any{}
	for key, v := range value {
		out[key] = v
	}
	return out
}
func stringPointer(value string) *string { return &value }
