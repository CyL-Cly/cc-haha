package ideintegration

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
	"strings"
	"testing"
	"time"
)

func TestIDEExtensionInstalledVersionPlatformParentFailuresAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Platform, Kind, VersionOutput, InstalledOutput, ParentPath string
		PluginInstalled                                            bool
		InstallResult                                              ExtensionProcessResult
	}
	rows := []row{}
	for _, platform := range []string{"macos", "linux", "windows"} {
		for _, kind := range []string{"vscode", "cursor", "windsurf", "pycharm", "unknown"} {
			for _, version := range []string{"", "anthropic.claude-code@2.1.200\n", "anthropic.claude-code@2.1.288\n", "other.extension@9.0.0\nanthropic.claude-code@2.1.400\n"} {
				rows = append(rows, row{Platform: platform, Kind: kind, VersionOutput: version, InstalledOutput: "anthropic.claude-code", PluginInstalled: true})
			}
		}
	}
	for _, code := range []int{1, 7} {
		rows = append(rows, row{Platform: "linux", Kind: "vscode", InstallResult: ExtensionProcessResult{Code: code, Error: "native process failed", Stderr: "fixture stderr"}})
	}
	rows = append(rows, row{Platform: "macos", Kind: "cursor", ParentPath: "/private/fixture/Visual Studio Code.app", VersionOutput: "anthropic.claude-code@2.1.288"})
	env := map[string]string{"DISPLAY": ":controlled", "HOME": "private-home", "KEEP": "private"}
	inputRows := []map[string]any{}
	for _, r := range rows {
		inputRows = append(inputRows, map[string]any{"platform": r.Platform, "kind": r.Kind, "versionOutput": r.VersionOutput, "installedOutput": r.InstalledOutput, "pluginInstalled": r.PluginInstalled, "parentPath": r.ParentPath, "global": map[string]any{"future": "retained"}, "installResult": map[string]any{"code": r.InstallResult.Code, "error": r.InstallResult.Error, "stderr": r.InstallResult.Stderr, "stdout": r.InstallResult.Stdout}})
	}
	home := t.TempDir()
	input, _ := json.Marshal(map[string]any{"homeDir": home, "environment": env, "version": "2.1.288", "cases": inputRows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-ide-extensions.fixture.ts")
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
		Installed bool
		Status    ExtensionStatus
		Global    map[string]any
		Requests  []struct {
			Command     string
			Args        []string
			Environment map[string]string
		}
	}
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	for i, r := range rows {
		requests := []struct {
			Command     string
			Args        []string
			Environment map[string]string
		}{}
		service := NewExtensions(ExtensionOptions{Home: home, Platform: r.Platform, Version: "2.1.288", Environment: env, ParentCommand: func(context.Context) string {
			if r.Platform == "macos" && r.ParentPath != "" {
				return r.ParentPath + "/Contents/Resources/app/bin/code"
			}
			return ""
		}, Stat: func(string) (fs.FileInfo, error) {
			if r.ParentPath != "" {
				return nil, nil
			}
			return nil, os.ErrNotExist
		}, ReadDir: func(string) ([]fs.DirEntry, error) {
			if r.PluginInstalled {
				return []fs.DirEntry{extensionFakeEntry{"PyCharm2026.1", true}}, nil
			}
			return nil, os.ErrNotExist
		}, Sleep: func(context.Context, time.Duration) error { return nil }, Run: func(_ context.Context, command string, args []string, environment map[string]string, timeout time.Duration) (ExtensionProcessResult, error) {
			if timeout != 10*time.Minute {
				t.Error("Source extension process default timeout differs", timeout)
			}
			requests = append(requests, struct {
				Command     string
				Args        []string
				Environment map[string]string
			}{command, args, environment})
			if strings.Contains(strings.Join(args, " "), "--install-extension") {
				return r.InstallResult, nil
			}
			if len(args) > 1 {
				return ExtensionProcessResult{Stdout: r.VersionOutput}, nil
			}
			return ExtensionProcessResult{Stdout: r.InstalledOutput}, nil
		}})
		// The native plugin FS primitive is controlled here; its complete actual
		// Source directory walk is tested separately on physical private dirs.
		if r.Kind == "pycharm" {
			service.options.Stat = func(string) (fs.FileInfo, error) {
				if r.PluginInstalled {
					return nil, nil
				}
				return nil, errors.New("ENOENT")
			}
		}
		installed := service.Installed(ctx, r.Kind)
		status := service.Install(ctx, r.Kind)
		if installed != expected[i].Installed || !reflect.DeepEqual(status, expected[i].Status) || !reflect.DeepEqual(requests, expected[i].Requests) {
			t.Fatalf("row%d %#v\nGo installed%v status%#v requests%#v\nSource%#v", i, r, installed, status, requests, expected[i])
		}
	}
}

type extensionFakeEntry struct {
	name      string
	directory bool
}

func (e extensionFakeEntry) Name() string { return e.name }
func (e extensionFakeEntry) IsDir() bool  { return e.directory }
func (e extensionFakeEntry) Type() fs.FileMode {
	if e.directory {
		return fs.ModeDir
	}
	return 0
}
func (e extensionFakeEntry) Info() (fs.FileInfo, error) { return nil, os.ErrNotExist }
