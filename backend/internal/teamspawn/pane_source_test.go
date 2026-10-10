package teamspawn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestActualPaneShutdownCommandsAndExitPolicyMatchSourcePhysicalSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Source physical POSIX subprocess fixture; Windows quoting has its existing native regression")
	}
	rows := []map[string]any{{"backend": "tmux", "pane": "%trusted", "external": true, "code": 0}, {"backend": "tmux", "pane": "%peer ; echo literal", "external": false, "code": 7}, {"backend": "iterm2", "pane": "actual-private-session", "external": true, "code": 0}}
	home := t.TempDir()
	directory := filepath.Join(home, "bin")
	log := filepath.Join(home, "args")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"tmux", "it2"} {
		if err := os.WriteFile(filepath.Join(directory, command), []byte("#!/bin/sh\nprintf '%s\\0' \"$@\" >> \"$CC_HAHA_PANE_LOG\"\nprintf '\\0' >> \"$CC_HAHA_PANE_LOG\"\nexit \"$CC_HAHA_PANE_EXIT\"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	actual := []bool{}
	for _, row := range rows {
		environment := map[string]string{"PATH": directory + ":/usr/bin:/bin", "HOME": home, "CC_HAHA_PANE_LOG": log, "CC_HAHA_PANE_EXIT": fmt.Sprint(row["code"])}
		if !row["external"].(bool) {
			environment["TMUX"] = "actual-launch-tmux"
		}
		service, err := NewPaneRuntime(home, os.Getpid(), environment)
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, service.KillPane(row["backend"].(string), row["pane"].(string)))
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	args := [][]string{}
	for _, record := range strings.Split(string(data), "\x00\x00") {
		if record == "" {
			continue
		}
		row := strings.Split(record, "\x00")
		for i, value := range row {
			if strings.HasPrefix(value, "claude-swarm-") {
				row[i] = "claude-swarm-PID"
			}
		}
		args = append(args, row)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	sourceHome, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-pane-kill.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_TEAM_PANE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": sourceHome, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		Rows         []bool
		Args         [][]string
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out), stderr.String())
	}
	if !reflect.DeepEqual(source.Rows, actual) || !reflect.DeepEqual(source.Args, args) {
		t.Fatal("actual Source pane subprocess differs", source.Rows, actual, source.Args, args)
	}
}
