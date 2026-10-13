package windowshell

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWindowsNativeInvocationAgainstActualCrossSpawn(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type input struct {
		Command     string            `json:"command"`
		File        string            `json:"commandFile"`
		Arguments   []string          `json:"arguments"`
		Environment map[string]string `json:"environment"`
	}
	args := []string{"", "normal", "中文 😀", "space value", "a&b|c>d", `C:\folder\`, `a\\"b`, "%PATH%", "a(b);*?[]!^`"}
	for n := 1; n <= 8; n++ {
		args = append(args, "slash"+strings.Repeat(`\`, n), "quote"+strings.Repeat(`\`, n)+`"end`)
	}
	rows := []input{}
	for _, file := range []string{`C:\Git\cmd\git.exe`, `C:\tool.COM`, `C:\Program Files\Code\bin\code.cmd`, `C:\repo\node_modules\.bin\code.cmd`, "foo/bar.cmd"} {
		for _, command := range []string{file, "code.cmd"} {
			rows = append(rows, input{command, file, args, map[string]string{"comspec": `C:\Windows\System32\cmd.exe`}})
		}
	}
	home := t.TempDir()
	data, _ := json.Marshal(map[string]any{"homeDir": home, "cases": rows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-spawn.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var expected []WindowsInvocation
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		actual := ProjectWindowsInvocation(row.Command, row.File, row.Arguments, row.Environment)
		if !reflect.DeepEqual(actual, expected[i]) {
			t.Fatalf("row%d\nactual%#v\nSource%#v", i, actual, expected[i])
		}
	}
}
