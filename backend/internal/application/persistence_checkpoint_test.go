package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// Source QueryEngine captures the CLI no-persistence flag separately from the
// writer's cleanupPeriodDays policy. Retention zero still produces checkpoints.
func TestActualMainPersistenceFlagAndRetentionZeroKeepDistinctCheckpointConsumers(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "retention-zero"
		if disabled {
			name = "no-session-persistence"
		}
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			var path string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					response(w, "policy-read", "Read", map[string]any{"file_path": path}, "")
				case 2:
					response(w, "policy-edit", "Edit", map[string]any{"file_path": path, "old_string": "BEFORE_POLICY_50DB", "new_string": "AFTER_POLICY_638B"}, "")
				case 3:
					response(w, "policy-done", "", nil, "POLICY_TOOL_DONE")
				default:
					t.Error("Unexpected extra model call")
					http.Error(w, "extra", 500)
				}
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.Tools = nil
			o.Arguments.SessionPersistenceDisabled = disabled
			o.Arguments.PermissionMode = "bypassPermissions"
			o.Native.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
			o.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
			if e := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"cleanupPeriodDays":0,"future":{"keep":true}}`), 0600); e != nil {
				t.Fatal(e)
			}
			path = filepath.Join(o.WorkDir, "persistence-policy.txt")
			if e := os.WriteFile(path, []byte("BEFORE_POLICY_50DB\n"), 0640); e != nil {
				t.Fatal(e)
			}
			s, e := NewSession(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			if e = s.Submit(context.Background(), tui.Input{Text: "Read and edit the private file."}, nil); e != nil {
				t.Fatal(e)
			}
			var history protocol.JSONObject
			if e = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				history = s.process.Controller.AppState().FileHistory
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			var snapshots []json.RawMessage
			if e = json.Unmarshal(history.Get("snapshots"), &snapshots); e != nil {
				t.Fatal(e, string(raw(history)))
			}
			if disabled && len(snapshots) != 0 || !disabled && len(snapshots) != 1 {
				t.Fatal("CLI flag was conflated with writer retention", disabled, string(raw(history)))
			}
			if !disabled && !strings.Contains(string(raw(history)), `completedFileBackups`) {
				t.Fatal("Retention zero skipped actual completed checkpoint", string(raw(history)))
			}
			if calls.Load() != 3 {
				t.Fatal(calls.Load())
			}
			data, e := os.ReadFile(path)
			if e != nil || string(data) != "AFTER_POLICY_638B\n" {
				t.Fatal("Actual tool did not execute", e, string(data))
			}
			_ = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, d os.DirEntry, e error) error {
				if e == nil && !d.IsDir() && filepath.Ext(path) == ".jsonl" {
					t.Error("Policy wrote canonical transcript", path)
				}
				return nil
			})
		})
	}
}
