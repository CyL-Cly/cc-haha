package teamspawn

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/iterm_setup.py
var itermSetupBinary string

func TestActualITermSetupPackageDetectionInstallHomeAndVerificationAgainstSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX original which/package manager commands")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{
		{"operation": "detect", "config": map[string]any{"available": map[string]bool{"uv": true, "pipx": true}}},
		{"operation": "detect", "config": map[string]any{"available": map[string]bool{"pipx": true}}},
		{"operation": "detect", "config": map[string]any{"available": map[string]bool{"pip3": true}}},
		{"operation": "detect", "config": map[string]any{}},
		{"operation": "install", "manager": "uvx", "config": map[string]any{}},
		{"operation": "install", "manager": "pipx", "config": map[string]any{"installError": map[string]string{"pipx": "physical private failure"}}},
		{"operation": "install", "manager": "pip", "config": map[string]any{"installError": map[string]string{"pip": "try real fallback"}}},
		{"operation": "install", "manager": "pip", "config": map[string]any{"installError": map[string]string{"pip": "first failed", "pip3": "second failed"}}},
		{"operation": "verify", "config": map[string]any{}},
		{"operation": "verify", "config": map[string]any{"available": map[string]bool{"it2": true}, "verifyError": "Python API not enabled"}},
		{"operation": "verify", "config": map[string]any{"available": map[string]bool{"it2": true}, "verifyError": "other failure"}},
		{"operation": "verify", "config": map[string]any{"available": map[string]bool{"it2": true}}},
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	bin := filepath.Join(home, "bin")
	work := filepath.Join(home, "work")
	_ = os.Mkdir(bin, 0700)
	_ = os.Mkdir(work, 0700)
	for _, name := range []string{"which", "uv", "pipx", "pip", "pip3", "it2"} {
		_ = os.WriteFile(filepath.Join(bin, name), []byte(itermSetupBinary), 0755)
	}
	log, config := filepath.Join(home, "log"), filepath.Join(home, "config")
	pane, _ := NewPaneRuntime(work, os.Getpid(), map[string]string{"HOME": home, "PATH": bin + ":/usr/bin:/bin", "CC_HAHA_ITERM_SETUP_LOG": log, "CC_HAHA_ITERM_SETUP_CONFIG": config})
	actual := []map[string]any{}
	for _, row := range rows {
		_ = os.WriteFile(config, rawJSON(row["config"]), 0600)
		_ = os.WriteFile(log, nil, 0600)
		var value any
		switch row["operation"] {
		case "detect":
			v := pane.DetectPythonPackageManager()
			if v != "" {
				value = v
			}
		case "install":
			value = pane.InstallITerm(row["manager"].(string))
		case "verify":
			value = pane.VerifyITerm()
		}
		data, _ := os.ReadFile(log)
		args := []json.RawMessage{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line != "" {
				args = append(args, json.RawMessage(line))
			}
		}

		actual = append(actual, map[string]any{"value": value, "args": args})
	}
	sourceHome, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-iterm-setup.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_TEAM_ITERM_SETUP_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": sourceHome, "rows": rows, "binaryScript": itermSetupBinary}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		Rows         json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out), stderr.String())
	}
	var a, b any
	_ = json.Unmarshal(rawJSON(actual), &a)
	_ = json.Unmarshal(source.Rows, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Source setup differs\nGo=%s\nSource=%s", rawJSON(actual), source.Rows)
	}
}
