package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCLIPrefillRetainsOriginalEarlyInputConsumption(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-prefill.fixture.ts")
	rows := []string{"", "   ", "\ufeff  draft 中文 😀 \n", "\u0085not-JS-whitespace\u0085", "first\nsecond", " /skill next "}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "BUN_OPTIONS=--no-env-file", "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_PREFILL_FIXTURE=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	out, e := command.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct {
		Rows []struct {
			HadInput                bool
			Consumed, SecondConsume string
			Capturing               bool
		}
	}
	if e := json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(out))
	}
	for i, seed := range rows {
		command, _, e := ParseCommand([]string{"--prefill", seed}, "fixture", home, home)
		if e != nil {
			t.Fatal(e)
		}
		actual := protocol.TrimJavaScriptString(command.Arguments.Prefill)
		want := source.Rows[i]
		if actual != want.Consumed || want.HadInput != (actual != "") || want.SecondConsume != "" || want.Capturing {
			t.Fatalf("Source actual early input changed %#v Go %q", want, actual)
		}
	}
}
