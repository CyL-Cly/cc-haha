package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWildcardRuleMatchesActualSourceEscapesAnchorsUnicodeAndPlaceholders(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	type row struct {
		Pattern json.RawMessage `json:"pattern"`
		Value   json.RawMessage `json:"value"`
	}
	rows := []row{}
	for _, pattern := range []string{`"*"`, `"git *"`, `"* run *"`, `" git * "`, `"a\\*b"`, `"a\\\\b"`, `"a\\?b"`, `"'\".+?^${}()|[]\\*"`, `"\u0000ESCAPED_STAR\u0000"`, `"\u0000ESCAPED_BACKSLASH\u0000"`, `"x\ud800*"`, `"a*b*c"`, `"\ufeff literal\u3000"`, `"*\n*"`} {
		for _, value := range []string{`""`, `"git"`, `"git status"`, `"git\nstatus"`, `"npm run"`, `"npm run test"`, `"a*b"`, `"a\\b"`, `"a?b"`, `"x\ud800trail"`, `"x\udffftrail"`, `"literal"`, `"literal\n"`, `"a\nb"`, `"\ud83d\ude00"`} {
			rows = append(rows, row{json.RawMessage(pattern), json.RawMessage(value)})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "wildcards": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []bool }
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, r := range rows {
		got, err := MatchWildcardRule(r.Pattern, r.Value)
		if err != nil || got != source.Rows[i] {
			t.Errorf("case%d pattern%s value%s Go%v %v Source%v", i, r.Pattern, r.Value, got, err, source.Rows[i])
		}
	}
}
