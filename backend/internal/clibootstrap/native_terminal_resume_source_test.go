package clibootstrap

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

func TestWholeOriginalTerminalResumeHandlerPhysicalLookupTitlesPickerAndAliasContract(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned Source Bun required")
	}
	const current = "11111111-1111-4111-8111-111111111111"
	const target = "22222222-2222-4222-8222-222222222222"
	makeFile := func(sid, title, prompt string) string {
		return `{"type":"user","uuid":"` + sid + `-user","parentUuid":null,"sessionId":"` + sid + `","timestamp":"2026-10-01T00:00:00.000Z","cwd":"$CWD","message":{"role":"user","content":"` + prompt + `"}}` + "\n" + `{"type":"assistant","uuid":"` + sid + `-answer","parentUuid":"` + sid + `-user","sessionId":"` + sid + `","timestamp":"2026-10-01T00:00:01.000Z","cwd":"$CWD","message":{"id":"api","role":"assistant","content":[{"type":"text","text":"original answer"}]}}` + "\n" + `{"type":"custom-title","sessionId":"` + sid + `","customTitle":"` + title + `"}` + "\n"
	}
	for _, row := range []struct {
		name, arg, entrypoint, message, rename string
		selectRow                              *int
		cancel, previewCapture                 bool
	}{{name: "uuid", arg: target, entrypoint: "slash_command_session_id"}, {name: "title", arg: " \ufeff OWNED TARGET \t", entrypoint: "slash_command_title"}, {name: "missing", arg: "not a title", message: "Session not a title was not found."}, {name: "missing-uuid", arg: "33333333-3333-4333-8333-333333333333", message: "Session 33333333-3333-4333-8333-333333333333 was not found."}, {name: "picker", selectRow: func() *int { v := 0; return &v }(), entrypoint: "slash_command_picker"}, {name: "preview-captured-full-log", previewCapture: true, selectRow: func() *int { v := 0; return &v }(), entrypoint: "slash_command_picker"}, {name: "rename-reload", rename: "new title from original disk", cancel: true, message: "Resume cancelled"}, {name: "cancel", cancel: true, message: "Resume cancelled"}} {
		t.Run(row.name, func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-resume.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_RESUME_SOURCE=1", "USER_TYPE=external", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Join(home, "bin") + ":/usr/bin:/bin", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-only-resume", "ANTHROPIC_BASE_URL=http://127.0.0.1:1"}
			input := map[string]any{"home": home, "currentSessionId": current, "argument": row.arg, "cancel": row.cancel, "rename": row.rename, "files": []any{map[string]any{"sid": current, "text": makeFile(current, "owned current", "CURRENT_ONLY"), "modified": 1790812901000}, map[string]any{"sid": target, "text": makeFile(target, "owned target", "TARGET_ONLY"), "modified": 1790812801000}}, "filterLogs": []any{map[string]any{"sessionId": current}, map[string]any{"sessionId": target}, map[string]any{"sessionId": "side", "isSidechain": true}}}
			if row.selectRow != nil {
				input["select"] = *row.selectRow
			}
			input["previewCapture"] = row.previewCapture
			cmd.Stdin = bytes.NewReader(rawMain(input))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("%v %.16000s", err, stderr.String())
			}
			var source struct {
				Resumed *struct {
					SID, Entrypoint string
					Log             protocol.JSONObject
				}
				Callbacks      []json.RawMessage
				Logs           []protocol.JSONObject
				Filtered       []protocol.JSONObject
				RenameReload   struct{ BeforeTitle, LoadingComponent, AfterTitle string }
				PreviewCapture struct {
					Captured, Fresh protocol.JSONObject
					PhysicalChanged bool
				}
				Caps struct{ Blocked []string }
			}
			if err = json.Unmarshal(output, &source); err != nil || len(source.Caps.Blocked) != 0 || len(source.Filtered) != 1 || source.Filtered[0].String("sessionId") != target {
				t.Fatal("actual Source resume", err, string(output))
			}
			if row.entrypoint != "" {
				if source.Resumed == nil || source.Resumed.SID != target || source.Resumed.Entrypoint != row.entrypoint || !bytes.Contains(source.Resumed.Log.Get("messages"), []byte("TARGET_ONLY")) || bytes.Contains(source.Resumed.Log.Get("messages"), []byte("CURRENT_ONLY")) {
					t.Fatal("actual original selection differs", string(output))
				}
				if len(source.Callbacks) != 1 || string(source.Callbacks[0]) != `[null,{"display":"skip"}]` {
					t.Fatal("Source skip completion", string(output))
				}
			}
			if row.message != "" {
				if source.Resumed != nil || len(source.Callbacks) != 1 || !strings.Contains(string(source.Callbacks[0]), row.message) {
					t.Fatal("original resume error/cancel", string(output))
				}
			}
			if row.selectRow != nil || row.cancel {
				if len(source.Logs) != 1 || source.Logs[0].String("sessionId") != target {
					t.Fatal("picker did not exclude active SID", string(output))
				}
			}
			if row.rename != "" && (source.RenameReload.BeforeTitle != "owned target" || source.RenameReload.LoadingComponent != "Box" || source.RenameReload.AfterTitle != row.rename) {
				t.Fatal("Actual original rename did not use the loading unmount and real fresh disk title", string(output))
			}
			if row.previewCapture && (!source.PreviewCapture.PhysicalChanged || !bytes.Contains(source.PreviewCapture.Fresh.Get("messages"), []byte("EXTERNAL_AFTER_PREVIEW_UNSEEN")) || bytes.Contains(source.PreviewCapture.Captured.Get("messages"), []byte("EXTERNAL_AFTER_PREVIEW_UNSEEN")) || bytes.Contains(source.Resumed.Log.Get("messages"), []byte("EXTERNAL_AFTER_PREVIEW_UNSEEN"))) {
				t.Fatal("Original preview confirmation did not retain its displayed full log", string(output))
			}
		})
	}
}
