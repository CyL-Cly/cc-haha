package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/recovery"
)

func TestEntrypointRecoveryAndUnmigratedMode(t *testing.T) {
	for _, fixture := range []struct {
		forced   bool
		args     []string
		out, err string
		code     int
	}{
		{forced: true, args: []string{"--help"}, out: recovery.Help},
		{forced: true, args: []string{"--version"}, out: recovery.Version},
		{forced: true, args: []string{"--print", "hello"}, err: "Error: set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN\n", code: 1},
		{forced: true, args: []string{"--system-prompt-file"}, err: "--system-prompt-file requires a file\n", code: 1},
		{args: []string{"--help"}, out: cliargs.Parse([]string{"--help"}, cliargs.Config{Version: "999.0.0-local"}).Stdout},
		{args: []string{"--version"}, out: "999.0.0-local (Claude Code)\n"},
		{args: []string{"-v"}, out: "999.0.0-local (Claude Code)\n"},
		{args: []string{"-V"}, out: "999.0.0-local (Claude Code)\n"},
	} {
		func() {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("CLAUDE_CONFIG_DIR", root)
			t.Setenv("ANTHROPIC_API_KEY", "")
			t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
			if fixture.forced {
				t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
			} else {
				t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "")
			}
			oldArgs, oldExit, oldIn, oldOut, oldErr := os.Args, exit, os.Stdin, os.Stdout, os.Stderr
			defer func() { os.Args, exit, os.Stdin, os.Stdout, os.Stderr = oldArgs, oldExit, oldIn, oldOut, oldErr }()
			in, err := os.CreateTemp(root, "stdin")
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			stdoutR, stdoutW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdoutR.Close()
			stderrR, stderrW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stderrR.Close()
			os.Stdin, os.Stdout, os.Stderr = in, stdoutW, stderrW
			os.Args = append([]string{"claude-haha"}, fixture.args...)
			code := -1
			exit = func(value int) { code = value }
			main()
			stdoutW.Close()
			stderrW.Close()
			stdout, _ := io.ReadAll(stdoutR)
			stderr, _ := io.ReadAll(stderrR)
			if code != fixture.code || string(stdout) != fixture.out || string(stderr) != fixture.err {
				t.Fatalf("code=%d stdout=%q stderr=%q; want=%+v", code, stdout, stderr, fixture)
			}
			if strings.Contains(string(stdout), "migration") {
				t.Fatal("diagnostic leaked into stdout")
			}
		}()
	}
}
