package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceClearFullSlashCallerCanonicalLocalReplay(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned actual Source required")
	}
	for _, alias := range []string{"clear", "reset", "new"} {
		t.Run(alias, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-clear.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLEAR_FIXTURE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "sid": "7f6cd65c-af93-4c2a-80a1-91fffc4f81c4", "tasks": []any{}, "transcript": "UNCHANGED SOURCE PARENT\n", "fullCaller": true, "alias": alias, "args": "kept command args", "renderCaller": true, "width": 132}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			var source struct {
				Caller struct {
					Messages, Normalized, NormalizedAPI []protocol.JSONObject
					ShouldQuery                         bool
				}
				OldTranscript  string
				RenderedCaller struct {
					Text  string
					Width int
				}
				Caps struct{ Blocked, SecureReads []string }
			}
			if err = json.Unmarshal(output, &source); err != nil || source.Caller.ShouldQuery || len(source.Caller.Messages) != 3 || len(source.Caller.Normalized) != 3 || len(source.Caller.NormalizedAPI) != 1 || source.OldTranscript != "UNCHANGED SOURCE PARENT\n" || len(source.Caps.Blocked) != 0 || !reflect.DeepEqual(source.Caps.SecureReads, []string{"owned-empty-legacy"}) {
				t.Fatal("Actual complete slash producer contract", err, string(output))
			}
			if source.RenderedCaller.Width != 132 || !strings.Contains(source.RenderedCaller.Text, "/clear kept command args") || !strings.Contains(source.RenderedCaller.Text, "(no content)") || strings.Contains(source.RenderedCaller.Text, "<command-name>") || strings.Contains(source.RenderedCaller.Text, "<local-command-stdout>") {
				t.Fatal("Original full slash result through mounted Message changed", source.RenderedCaller.Text)
			}
			o := nativeSessionOptions(t, "http://127.0.0.1:9")
			o.Interactive = true
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if err = s.Submit(ctx, tui.Input{Text: "/" + alias + " kept command args", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil {
				t.Fatal(err)
			}
			semantic := func(rows []protocol.JSONObject) []map[string]any {
				out := make([]map[string]any, len(rows))
				for i, row := range rows {
					api, _ := row.Object("message")
					out[i] = map[string]any{"type": row.String("type"), "subtype": row.String("subtype"), "isMeta": string(row.Get("isMeta")), "role": api.String("role"), "messageContent": api.String("content"), "content": row.String("content"), "level": row.String("level")}
				}
				return out
			}
			if actual := s.process.Controller.History(); !reflect.DeepEqual(semantic(actual), semantic(source.Caller.Messages)) {
				t.Fatal("Actual Go local replay differs original complete slash producer", semantic(actual), semantic(source.Caller.Messages))
			}
		})
	}
}
