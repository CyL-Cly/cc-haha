package processpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestWindowsTreeAbsoluteExecutableAndBoundedFallbackAgainstActualSource(t *testing.T) {
	rows := []struct {
		Env     map[string]string `json:"env"`
		PID     int               `json:"pid"`
		Failure string            `json:"failure"`
	}{
		{map[string]string{"SystemRoot": `C:\Windows`, "PATH": `C:\private-project`}, 42, ""},
		{map[string]string{"sYsTeMrOoT": `D:\WinNT\`, "windir": `E:\ignored`}, 100, ""},
		{map[string]string{"SystemRoot": "", "WINDIR": `E:/Windows/../Windows`}, 4, "exit"},
		{map[string]string{"SystemRoot": `\\server\share\Windows`}, 5, "start"},
		{map[string]string{"SystemRoot": `C:\Windows`}, 6, "timeout"},
		{map[string]string{"PATH": `C:\private-project`}, 9, ""},
		{map[string]string{"SystemRoot": `C:\Windows`}, 0, ""},
	}
	type result struct {
		Executable *string        `json:"executable"`
		OK         bool           `json:"ok"`
		Command    []string       `json:"command"`
		Options    map[string]any `json:"options"`
		Killed     bool           `json:"killed"`
	}
	got := []result{}
	for _, row := range rows {
		keys := []string{}
		for k := range row.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		env := []string{}
		for _, k := range keys {
			env = append(env, k+"="+row.Env[k])
		}
		path := ResolveWindowsTaskkill(env)
		out := result{}
		if path != "" {
			out.Executable = &path
		}
		out.OK = KillWindowsProcessTree(context.Background(), row.PID, WindowsTreeKillOptions{Environment: env, Timeout: 3 * time.Millisecond, Run: func(ctx context.Context, p string, args, received []string) error {
			out.Command = append([]string{p}, args...)
			out.Options = map[string]any{"stdin": "ignore", "stdout": "ignore", "stderr": "ignore", "windowsHide": true}
			if !reflect.DeepEqual(received, env) {
				t.Fatal("tree killer inherited ambient environment")
			}
			switch row.Failure {
			case "start", "exit":
				return errors.New("fixture native failure")
			case "timeout":
				<-ctx.Done()
				out.Killed = true
				return context.Cause(ctx)
			}
			return nil
		}})
		got = append(got, out)
	}
	if got[0].Executable == nil || *got[0].Executable != `C:\Windows\System32\taskkill.exe` || !got[0].OK {
		t.Fatal(got[0])
	}
	if got[4].OK || !got[4].Killed || got[5].Command != nil || got[6].Command != nil {
		t.Fatal("timeout/invalid inputs escaped fallback", got)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Go boundaries passed; actual Source needs pinned Bun")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-tree.fixture.ts")
	raw, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WINDOWS_TREE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source Windows tree %v %.3000s", err, output)
	}
	var expected []result
	if err = json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err, string(output))
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("Go %#v\nSource %#v", got, expected)
	}
}
