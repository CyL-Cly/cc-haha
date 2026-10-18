package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestTerminalStickersActualSourcePhysicalBrowserCanonicalAndNextHarness(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned Source required")
	}
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			f := worktreeMainFixture(t)
			browser, receipt := filepath.Join(f.home, "stickers-browser"), filepath.Join(f.home, "stickers-receipt")
			script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$STICKERS_RECEIPT\"\n"
			if failure {
				script += "exit 19\n"
			}
			if err := os.WriteFile(browser, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			f.n.Environment["BROWSER"] = browser
			f.n.Environment["STICKERS_RECEIPT"] = receipt
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-stickers.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = f.home
			cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_STICKERS_SOURCE=1"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "environment": f.n.Environment}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Actual Source browser %v %.3000s", err, output)
			}
			expected := mainObj(output).String("value")
			original, err := os.ReadFile(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(receipt); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if !bytes.Contains(body, []byte(expected)) || !bytes.Contains(body, []byte("<command-name>/stickers")) {
					t.Error("Canonical local result absent in next actual request", string(body))
				}
				emitWorktreeModel(w, 1, "", "", "STICKERS_NEXT_DONE")
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			seedNativeReleaseWarmFixture(t, f, "fixture")
			disabled := false
			var stdout, diagnostics bytes.Buffer
			m, err := NewMainPrint(MainPrintOptions{Interactive: true, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual stickers and next Harness", Tools: []string{"Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: &stdout, Error: &diagnostics})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			user := func(text string) protocol.JSONObject {
				return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
			}
			if err = m.RunTurn(ctx, user("/stickers ignored arguments")); err != nil {
				t.Fatalf("Missing actual native Source local command: %v", err)
			}
			actual, err := os.ReadFile(receipt)
			if err != nil || !bytes.Equal(actual, original) || calls.Load() != 0 {
				t.Fatal("Actual browser primitive", err, string(actual), string(original), calls.Load())
			}
			if err = m.RunTurn(ctx, user("STICKERS_NEXT_HUMAN")); err != nil {
				t.Fatal(err, diagnostics.String())
			}
			if calls.Load() != 1 {
				t.Fatal("Unexpected model requests", calls.Load())
			}
		})
	}
}
