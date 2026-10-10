package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeBashHookIfActualSourcePreparedMatcher(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	commands := []string{"", "git", "git push", "git add x && git push", "ls && git push", "FOO=bar git push", "git push | cat", "SUB=push; git $SUB", "V=$(date); echo \"prefix:$V\"", "echo \"now: $(date)\"", "echo $(date)", "echo \"$HOME\"", "cat <<'EOF'\ngit push\nEOF", "cat <<EOF\ntext\nEOF", "echo \"git\npush\"", "if true;then git push;else echo yes;fi", "case x in a)git push;;esac", "eval x", "git \"push force\"", "git ''", "git pushx", "V=safe; V+=next; git $V", "for i in one;do git $i;done", "git \"emoji:😀\"", strings.Repeat(" ", 10001), "a\u0085b"}
	patterns := []json.RawMessage{quote("git"), quote("git:*"), quote("git push:*"), quote("git *"), quote("git push"), quote("git push *"), quote("* push *"), quote("git\\*"), quote("git :*"), quote(":*"), quote(" git:* "), quote("echo*"), quote("date:*"), quote("cat:*"), quote("git\npush:*"), quote("*"), json.RawMessage(`"\ud800:*"`), quote("git *\n"), quote("git push\\*")}
	type row struct {
		Command  string            `json:"command"`
		Enabled  bool              `json:"enabled"`
		Patterns []json.RawMessage `json:"patterns"`
	}
	rows := []row{}
	for _, enabled := range []bool{false, true} {
		for _, command := range commands {
			rows = append(rows, row{command, enabled, patterns})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "bashIfMatchers": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows [][]bool }
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(out))
	}
	t.Logf("actual Source prepared Bash profiles=%d comparisons=%d", len(rows), len(rows)*len(patterns))
	for i, row := range rows {
		matcher, err := nativeBashHookIfMatcher(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quote(row.Command)}), row.Enabled)
		if err != nil {
			t.Fatal(err)
		}
		for j, pattern := range patterns {
			matched, err := matcher(pattern)
			if err != nil || matched != source.Rows[i][j] {
				t.Errorf("case%d/%d command%q enabled%v pattern%s Go%v err%v Source%v", i, j, row.Command, row.Enabled, pattern, matched, err, source.Rows[i][j])
			}
		}
	}
}
