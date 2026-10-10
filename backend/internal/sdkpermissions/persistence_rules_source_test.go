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

func TestPersistedPermissionRuleNormalizationMatchesActualSourceUTF16(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	norm := []json.RawMessage{}
	for _, s := range []string{"Task", "Task(*)", "Task()", "KillShell", "AgentOutputTool(*)", "BashOutputTool(nested (x))", "Bash(*)", "Bash(**)", "Bash(echo \\(x\\))", "Bash(echo \\x)", "Bash(echo \\\\(x\\\\))", "Read(path\\(foo\\)\\\\bar)", "Read(path(x)y)", "Read(path(x))tail", "Read(", "Read)", "(foo)", "()", "", "task", "mcp__server", "mcp__server(*)", "mcp__server__tool", "\\(escaped", "Read(x\\))", "Read(x\\\\))"} {
		norm = append(norm, quote(s))
	}
	norm = append(norm, json.RawMessage(`"Read(\ud800)"`), json.RawMessage(`"\ud800(*)"`), json.RawMessage(`"Task(\udc00\\(x\\))"`), json.RawMessage(`"Bash(\ud83d\ude00)"`))
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-permission-persistence.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PERMISSION_PERSISTENCE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": []any{}, "normalize": norm})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source rules %v %.3000s", e, stderr.String())
	}
	var result struct {
		Normalized   []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &result); e != nil || len(result.Normalized) != len(norm) || len(result.Capabilities.Blocked) > 0 {
		t.Fatalf("Source rules %v %.3000s", e, out)
	}
	for i, raw := range norm {
		value, e := normalizePersistedRule(raw)
		if e != nil || rawStringIdentity(value) != rawStringIdentity(result.Normalized[i]) {
			t.Errorf("case%d input%s Go%s Source%s error%v", i, raw, value, result.Normalized[i], e)
		}
	}
}
