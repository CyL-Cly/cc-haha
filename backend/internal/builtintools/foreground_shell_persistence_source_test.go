package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestForegroundBashPersistedPreviewMappingMatchesActualSource(t *testing.T) {
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
	values := []json.RawMessage{quote(""), quote("\n\nshort original\n\n"), quote(strings.Repeat("x", 1999)), quote(strings.Repeat("x", 2000)), quote(strings.Repeat("x", 2001)), quote(strings.Repeat("x", 1400) + "\n" + strings.Repeat("y", 1800)), protocol.QuoteJavaScriptUTF16(append(utf16.Encode([]rune(strings.Repeat("x", 1999)+"👨")), 0xd800)), quote(strings.Repeat("界👨‍👩‍👧é\n", 1000))}
	rows := []protocol.JSONObject{}
	for _, value := range values {
		rows = append(rows, protocol.NewJSONObject(field("stdout", json.RawMessage(nil)), field("stderr", ""), field("interrupted", false), field("isImage", false), field("persistedOutputPath", filepath.Join(home, "private-output.txt")), field("persistedOutputSize", int64(123456))))
		rows[len(rows)-1].Set("stdout", value)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-foreground-shell.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_FOREGROUND_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": cwd, "largeOutputs": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.4000s", e, out)
	}
	var source struct {
		LargeOutputs []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.LargeOutputs) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.4000s", e, out)
	}
	for i, row := range rows {
		got, e := MapResult("Bash", raw(row), quote("original-large-bash"))
		if e != nil || string(protocol.StringifyValidatedJavaScriptJSON(raw(got))) != string(protocol.StringifyValidatedJavaScriptJSON(source.LargeOutputs[i])) {
			t.Errorf("case%d Go%s/%v Source%s", i, raw(got), e, source.LargeOutputs[i])
		}
	}
}
