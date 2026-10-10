package clilauncher

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

func TestActualSourceCLIPathBlocksWindowsWrapperAndPhysicalUnixInstall(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	source := filepath.Join(home, "bundle's directory", "claude-sidecar-go")
	_ = os.MkdirAll(filepath.Dir(source), 0700)
	_ = os.WriteFile(source, []byte("#!/bin/sh\nexit 0\n"), 0755)
	config := filepath.Join(home, ".claude")
	formatting := []map[string]any{}
	for _, shell := range []string{"zsh", "bash", "fish"} {
		for _, platform := range []string{"darwin", "linux", "win32"} {
			formatting = append(formatting, map[string]any{"shell": shell, "platform": platform, "binDir": filepath.Join(home, ".local", "bin"), "pathValue": `"%USERPROFILE%/.local/bin\\";/other`, "source": source, "existing": "preserve unknown config\n\n" + pathStart + "\nold\n" + pathEnd + "\nTAIL\n", "block": "new block\ufeff\n"})
		}
	}
	ensure := []map[string]any{}
	for _, env := range []map[string]string{{"CLAUDE_CLI_PATH": filepath.Join(home, "src", "cli.ts"), "SHELL": "/bin/zsh", "PATH": "/usr/bin:/bin"}, {"CLAUDE_CLI_PATH": source, "SHELL": "/bin/zsh", "ZDOTDIR": filepath.Join(home, "zsh-owned"), "PATH": "/usr/bin:/bin"}, {"CLAUDE_CLI_PATH": source, "SHELL": "/bin/fish", "PATH": filepath.Join(home, ".local", "bin") + ":/bin"}} {
		ensure = append(ensure, map[string]any{"environment": env})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-launcher.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "formatting": formatting, "ensure": ensure})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_CLI_LAUNCHER_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %s", e, stderr.String())
	}
	var sourceResult struct {
		Formatting   []map[string]any
		Statuses     []Status
		Capabilities struct{ Blocked []any }
	}
	if e = json.Unmarshal(output, &sourceResult); e != nil || len(sourceResult.Formatting) != len(formatting) || len(sourceResult.Statuses) != len(ensure) || len(sourceResult.Capabilities.Blocked) > 0 {
		t.Fatal("Source shape", e, string(output))
	}
	for i, row := range formatting {
		platform := row["platform"].(string)
		commandName := command
		if platform == "win32" {
			commandName += ".cmd"
			platform = "windows"
		}
		got := map[string]any{"command": commandName, "pathPresent": PathPresent(row["pathValue"].(string), row["binDir"].(string), platform, home), "block": ManagedPathBlock(row["shell"].(string), row["binDir"].(string), home), "upserted": UpsertPathBlock(row["existing"].(string), row["block"].(string)), "windowsWrapper": WindowsWrapper(source, config)}
		if !reflect.DeepEqual(got, sourceResult.Formatting[i]) {
			t.Errorf("format%d Go%v Source%v", i, got, sourceResult.Formatting[i])
		}
	}
	wrapperSource, e := os.ReadFile(filepath.Join(home, ".local", "bin", command))
	if e != nil {
		t.Fatal(e)
	}
	if string(wrapperSource) != UnixWrapper(source, config) {
		t.Errorf("actual Source Unix wrapper mismatch\nGo%s\nSource%s", UnixWrapper(source, config), wrapperSource)
	}
	for i, row := range ensure {
		env := row["environment"].(map[string]string)
		env["HOME"] = home
		env["CLAUDE_CONFIG_DIR"] = config
		service, e := New(Options{HomeDir: home, Platform: "darwin", Environment: func() map[string]string { return env }})
		if e != nil {
			t.Fatal(e)
		}
		got := service.Ensure(ctx)
		if !reflect.DeepEqual(got, sourceResult.Statuses[i]) {
			t.Errorf("ensure%d Go%+v Source%+v", i, got, sourceResult.Statuses[i])
		}
	}
}
