package sessiontitle

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

func TestActualOriginalSessionTitleCleaningNormalizationAndGraphemeFallback(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned Source Bun required")
	}
	values := []json.RawMessage{json.RawMessage(`""`), json.RawMessage(`"   \t\n\u00a0\ufeff"`), json.RawMessage(`"alpha\ud800 beta\udfff"`), json.RawMessage(`"<command-name>/rename</command-name><command-args>  A &amp; &lt;B&gt; &quot;C&quot; &#39;D&#39; </command-args>"`), json.RawMessage(`"<COMMAND-NAME>/remote</COMMAND-NAME> <command-args> a\u00a0b </command-args> ignored"`), json.RawMessage(`"before <system-reminder>private\n<inner>x</inner></system-reminder> after"`), json.RawMessage(`"<system-reminder>missing close"`), json.RawMessage(`"<XML>upper case remains</XML>"`), json.RawMessage(`"<tag\u00a0key='value'>strip</tag> alpha\u0085omega"`), json.RawMessage(`"<command-name> &amp;lt;safe&amp;gt; </command-name><command-args>\ud800</command-args>"`)}
	for _, value := range []string{strings.Repeat("a", 80), strings.Repeat("a", 81), strings.Repeat("😀", 40), strings.Repeat("😀", 41), strings.Repeat("界", 50), strings.Repeat("👩🏽‍💻", 40), strings.Repeat("e\u0301", 70), "\x1b[31m" + strings.Repeat("q", 90) + "\x1b[0m"} {
		raw, _ := json.Marshal(value)
		values = append(values, raw)
	}
	values = append(values, json.RawMessage(`"`+strings.Repeat("a", 74)+`\ud800 MORE"`))
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-title-text.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SESSION_TITLE_TEXT_SOURCE=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "values": values})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Rows    []struct{ Input, Clean, Normalized, Truncated json.RawMessage }
		Blocked []string
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(values) || len(source.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	equal := func(a, b json.RawMessage) bool {
		return bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b))
	}
	for _, row := range source.Rows {
		t.Run(string(row.Input), func(t *testing.T) {
			cleaned, err := CleanRaw(row.Input)
			if err != nil || !equal(cleaned, row.Clean) {
				t.Fatalf("clean %s / %s %v", cleaned, row.Clean, err)
			}
			normalized, valid, err := NormalizeRaw(row.Input)
			if !valid {
				normalized = json.RawMessage(`null`)
			}
			if err != nil || !equal(normalized, row.Normalized) {
				t.Fatalf("normalized %s / %s %v", normalized, row.Normalized, err)
			}
			truncated, err := TruncateRaw(row.Input, 75)
			if err != nil || !equal(truncated, row.Truncated) {
				t.Fatalf("truncate %s / %s %v", truncated, row.Truncated, err)
			}
		})
	}
	for _, invalid := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`), json.RawMessage(`"bad`)} {
		if _, err := CleanRaw(invalid); err == nil {
			t.Fatalf("invalid title admitted %s", invalid)
		}
	}
}
