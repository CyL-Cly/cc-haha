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

func TestWholeOriginalTerminalBranchCommandBuildsOriginalCapturedForkLog(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual original Source Bun unavailable")
	}
	const sid = "11111111-1111-4111-8111-111111111111"
	parent := `{"type":"user","uuid":"user","parentUuid":null,"sessionId":"` + sid + `","timestamp":"2026-10-01T01:00:00.000Z","message":{"role":"user","content":"  original\n\t branch prompt  "}}` + "\n" + `{"type":"assistant","uuid":"answer","parentUuid":"user","sessionId":"` + sid + `","timestamp":"2026-10-01T01:00:01.000Z","message":{"id":"original-api","role":"assistant","content":[{"type":"text","text":"original answer"}]}}` + "\n" + `{"type":"agent-name","sessionId":"` + sid + `","agentName":"must not enter forkLog"}` + "\n"
	for _, argument := range []string{"", "  original branch \ufeff "} {
		t.Run(argument, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-branch.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_BRANCH_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-only-branch", "ANTHROPIC_BASE_URL=http://127.0.0.1:1"}
			command.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "sessionId": sid, "parent": parent, "argument": argument, "holdResume": true}))
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var source struct {
				Resumed struct {
					SessionID, Entrypoint string
					Log                   protocol.JSONObject
				}
				Callbacks           []json.RawMessage
				ParentUnchanged     bool
				Fork                string
				Caps                struct{ Blocked []string }
				BeforeResumeRelease struct {
					Callbacks   int
					ForkCreated bool
				}
			}
			if err = json.Unmarshal(output, &source); err != nil || !source.ParentUnchanged || len(source.Callbacks) != 1 || len(source.Caps.Blocked) != 0 {
				t.Fatal("actual full Source branch receipt", err, string(output), stderr.String())
			}
			if source.BeforeResumeRelease.Callbacks != 0 || !source.BeforeResumeRelease.ForkCreated {
				t.Fatal("actual Source onDone preceded held resume although file was ready", source.BeforeResumeRelease)
			}
			log := source.Resumed.Log
			if source.Resumed.Entrypoint != "fork" || source.Resumed.SessionID == sid || log.String("firstPrompt") != "original branch prompt" || log.String("sessionId") != source.Resumed.SessionID {
				t.Fatal("actual Source callback differs", source.Resumed)
			}
			keys := []string{}
			for _, p := range log.OwnProperties() {
				var key string
				_ = json.Unmarshal(p.NameJSON, &key)
				keys = append(keys, key)
			}
			want := []string{"date", "messages", "fullPath", "value", "created", "modified", "firstPrompt", "messageCount", "isSidechain", "sessionId", "customTitle", "contentReplacements"}
			if !reflect.DeepEqual(keys, want) {
				t.Fatal("Source forkLog's actual field omissions changed", keys)
			}
			var callback []json.RawMessage
			if err = json.Unmarshal(source.Callbacks[0], &callback); err != nil || len(callback) != 2 {
				t.Fatal(err, source.Callbacks)
			}
			text := "Branched conversation"
			if argument != "" {
				text += " \"original branch\""
			}
			text += ". You are now in the branch.\nTo resume the original: claude -r " + sid
			if string(callback[0]) != string(quoteMain(text)) || string(callback[1]) != `{"display":"system"}` {
				t.Fatal("actual Source completion changed", string(source.Callbacks[0]))
			}
		})
	}
}
