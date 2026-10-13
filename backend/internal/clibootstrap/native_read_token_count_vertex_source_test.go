package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceVertexReadCountingBetaAllowlist(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	models := []string{"claude-haiku-4-5", "claude-sonnet-4-5[1m]", "claude-sonnet-5", "claude-fable-5-1"}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			allocation := t.TempDir()
			home := filepath.Join(allocation, "source-home")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"CLAUDE_CODE_USE_VERTEX": "1", "CLAUDE_CODE_SKIP_VERTEX_AUTH": "1", "ANTHROPIC_VERTEX_PROJECT_ID": "private-project", "ANTHROPIC_BETAS": "unallowed-count-beta,interleaved-thinking-2025-05-14,context-management-2025-06-27,context-management-2025-06-27"}
			input, _ := json.Marshal(map[string]any{"home": home, "url": "http://127.0.0.1:1", "session": "11111111-1111-4111-8111-111111111111", "device": "private-device", "model": model, "env": env, "rows": []any{}, "includeBetas": true})
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-read-token-count.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_READ_TOKEN_COUNT_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var actual struct{ Betas []string }
			if err := json.Unmarshal(output, &actual); err != nil {
				t.Fatal(err, string(output))
			}
			want := []string{}
			for _, beta := range actual.Betas {
				if beta == "claude-code-20250219" || beta == "interleaved-thinking-2025-05-14" || beta == "context-management-2025-06-27" {
					want = append(want, beta)
				}
			}
			got, err := nativeReadCountingBetas(model, env, protocol.NewJSONObject(), false)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("Source Vertex counting beta mismatch", got, want)
			}
		})
	}
}
