package builtintools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestForegroundShellFileProgressMatchesActualSourceTaskOutput(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(cwd, 0700)
	_ = os.MkdirAll(temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	data := [][]byte{nil, []byte("one"), []byte("one\n"), []byte("one\ntwo\nthree\nfour\nfive\nsix\n"), []byte(strings.Repeat("dense\n", 2000)), []byte(strings.Repeat("界👨‍👩‍👧é\n", 1000)), []byte("\xff\xfe\x00one\n")}
	rows := []map[string]string{}
	for i, value := range data {
		id := "b0123abc" + string(rune('0'+i))
		rows = append(rows, map[string]string{"id": id, "base64": base64.StdEncoding.EncodeToString(value)})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-foreground-shell.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_FOREGROUND_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": cwd, "samples": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.4000s", e, out)
	}
	var source struct {
		Samples []struct {
			Last, Full   string
			Lines, Bytes int64
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Samples) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.4000s", e, out)
	}
	for i, value := range data {
		path := filepath.Join(temp, rows[i]["id"]+".output")
		if e = os.WriteFile(path, value, 0600); e != nil {
			t.Fatal(e)
		}
		got, e := readForegroundShellProgress(path, 0)
		want := source.Samples[i]
		if e != nil || got.last != want.Last || got.full != want.Full || got.lines != want.Lines || got.bytes != want.Bytes {
			t.Errorf("sample%d Go%+v/%v Source%+v", i, got, e, want)
		}
	}
}
