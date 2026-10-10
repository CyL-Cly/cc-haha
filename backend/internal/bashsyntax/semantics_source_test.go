package bashsyntax

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSemanticsActualSourceFlagOperandAndUTF16Boundaries(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	rows := [][]SecurityCommand{}
	add := func(argv ...SecurityText) {
		rows = append(rows, []SecurityCommand{{Argv: argv, EnvVars: []SecurityEnvVar{}, Redirects: []SecurityRedirect{}, Text: "original"}})
	}
	for _, duration := range []SecurityText{"5", "5s", "5.5", "5e-1", ".5", "+5", "inf", "5\n", "5\r", "5\u2028", "5\u2029", "5\u00a0", "", "__TRACKED_VAR__"} {
		for _, flags := range [][]SecurityText{nil, {"--foreground"}, {"--signal=TERM"}, {"--signal", "TERM"}, {"-sTERM"}, {"-s", "TERM"}, {"-k", "5"}, {"-v"}, {"--unknown"}, {"--signal", "bad value"}, {"-k", ""}, {"-z"}} {
			argv := []SecurityText{"timeout"}
			argv = append(argv, flags...)
			argv = append(argv, duration, "eval", "x")
			add(argv...)
		}
	}
	for _, flag := range []SecurityText{"-o", "-o0", "-o\n", "-o\r", "-o\u2028", "--output=0", "--output", "-iL", "-e0", "-x", ""} {
		for _, next := range []SecurityText{"0", "eval", "", "\n", "__TRACKED_VAR__"} {
			add("stdbuf", flag, next, "eval", "x")
		}
	}
	for _, name := range []SecurityText{"test", "[", "[[", "printf", "read", "unset", "wait"} {
		for _, flag := range []SecurityText{"-v", "-vNAME[x]", "-rav", "-ra", "-a", "-p", "-rp", "-p[prompt]", "--", "-R", "-u", "-N"} {
			for _, operand := range []SecurityText{"plain", "a[x]", "a[$(id)]", "[prompt]", ""} {
				add(name, flag, operand, "data")
			}
		}
	}
	for _, operand := range []SecurityText{"plain", "a[x]", "a[$(id)]"} {
		for _, op := range []SecurityText{"-eq", "-ne", "-lt", "-le", "-gt", "-ge", "==", "=~"} {
			add("[[", operand, op, "0")
			add("[[", "0", op, operand)
		}
	}
	for _, value := range []SecurityText{"system(x)", "asystem(x)", "system (x)", "system\u00a0(x)", "system\u2028(x)", "--rawfile=x", "--slurpfile", "--from-file", "-f", "-fpath", "-f/path", "-L", "-L/x", "-Lpath", "prefix\n#hidden"} {
		add("jq", value)
	}
	for _, name := range []SecurityText{"fc", "compgen", "command"} {
		for _, flag := range []SecurityText{"-l", "-ln", "-s", "-e", "-E", "--", "-c", "-C", "-f", "-F", "-W", "-v", "-V", "-CW", "-r\u2028C"} {
			add(name, flag, "data")
		}
	}
	for _, r := range []json.RawMessage{json.RawMessage(`"a\ud800"`), json.RawMessage(`"\udc00"`), json.RawMessage(`"😀"`)} {
		var value SecurityText
		if err := json.Unmarshal(r, &value); err != nil {
			t.Fatal(err)
		}
		add("echo", value)
		add("jq", "system"+value+"(x)")
	}
	for _, placement := range []string{"argv", "env", "redirect"} {
		cmd := SecurityCommand{Argv: []SecurityText{"echo", "x"}, EnvVars: []SecurityEnvVar{}, Redirects: []SecurityRedirect{}, Text: "original"}
		switch placement {
		case "argv":
			cmd.Argv = append(cmd.Argv, "a\n#hidden")
		case "env":
			cmd.EnvVars = append(cmd.EnvVars, SecurityEnvVar{"V", "a\n#hidden"})
		case "redirect":
			cmd.Redirects = append(cmd.Redirects, SecurityRedirect{Op: ">", Target: "a\n#hidden"})
		}
		rows = append(rows, []SecurityCommand{cmd})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "bashSemantics": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []json.RawMessage }
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(out))
	}
	t.Logf("actual Source isolated semantics cases: %d", len(rows))
	for i, row := range rows {
		actual, _ := json.Marshal(CheckSemantics(row))
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(source.Rows[i])) {
			t.Errorf("case%d argv%v Go%s Source%s", i, row[0].Argv, actual, source.Rows[i])
		}
	}
}
