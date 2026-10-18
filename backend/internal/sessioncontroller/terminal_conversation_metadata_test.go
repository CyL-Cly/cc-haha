package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualSourceTerminalMetadataExistingOldTranscriptMissingCleanupAndRawReset(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source required")
	}
	owner, writer, pending := makePending(t, OSFiles{})
	if err := beginPending(t, owner, pending).Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := bootstrapstate.New(owner, writer.options.SessionID, writer.options.CWD, func() (string, error) { return writer.options.SessionID, nil })
	if err != nil {
		t.Fatal(err)
	}
	writer.options.Bootstrap = state
	controller := &Controller{writer: writer, options: Options{Bootstrap: state}}
	path := writer.Snapshot().String("path")
	initial := []byte(`{"type":"custom-title","customTitle":"old-title","future":{"preserve":17}}` + "\n")
	if err := os.WriteFile(path, initial, 0644); err != nil {
		t.Fatal(err)
	}
	var sid string
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { sid = writer.currentSessionID(); return nil })
	actions := []map[string]any{{"name": "saveCustomTitle", "value": "new-title"}, {"name": "saveAgentName", "value": "new-title"}, {"name": "saveAgentColor", "value": "pink"}, {"name": "saveAgentColor", "value": "default"}, {"name": "saveTag", "value": "typed-tag"}, {"name": "saveTag", "value": ""}, {"name": "saveAgentName", "value": json.RawMessage(`"\ud800-name"`)}}
	rows := []any{map[string]any{"initial": string(initial), "actions": actions}, map[string]any{"missing": true, "actions": actions}, map[string]any{"disabled": true, "initial": string(initial), "actions": actions}}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-conversation-metadata.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_METADATA_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "sid": sid, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("actual original metadata producer", err, string(output))
	}
	var source struct {
		Results []struct {
			Exists   bool
			Content  string
			Mode     uint32
			Metadata map[string]json.RawMessage
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != 3 || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatal("invalid guarded Source result", err, string(output))
	}
	for index, row := range source.Results {
		if index == 1 {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if index == 2 {
			if err := os.WriteFile(path, initial, 0644); err != nil {
				t.Fatal(err)
			}
			cache := writer.options.SettingsState.GetSessionSettingsCache()
			cache.Settings.Set("cleanupPeriodDays", json.RawMessage("0"))
		}
		for _, action := range actions {
			kind := map[string]string{"saveCustomTitle": "custom-title", "saveAgentName": "agent-name", "saveAgentColor": "agent-color", "saveTag": "tag"}[action["name"].(string)]
			value, _ := json.Marshal(action["value"])
			receipt, err := owner.SubmitIndependent(context.Background(), cooperative.Input, func(scope *cooperative.Scope) error {
				return controller.SaveTerminalMetadataJSONOnOwner(scope, kind, value)
			})
			if err == nil {
				err = receipt.Wait()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		contents, readErr := os.ReadFile(path)
		if row.Exists {
			info, err := os.Stat(path)
			if err != nil || readErr != nil || string(contents) != row.Content || uint32(info.Mode().Perm()) != row.Mode {
				t.Fatal("Source exact old/eager/cleanup transcript mismatch", index, err, readErr, string(contents), row.Content)
			}
		} else if !os.IsNotExist(readErr) {
			t.Fatal("metadata-only transcript created", index, readErr)
		}
		pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
			metadata, err := controller.TerminalMetadataOnOwner(scope)
			if err != nil {
				return err
			}
			for sourceName, field := range map[string]string{"currentSessionTitle": "customTitle", "currentSessionAgentName": "agentName", "currentSessionAgentColor": "agentColor", "currentSessionTag": "tag"} {
				if !bytes.Equal(metadata.Get(field), row.Metadata[sourceName]) {
					t.Fatal("actual cache lost Source reset/raw Unicode", index, field, string(metadata.Get(field)), string(row.Metadata[sourceName]))
				}
			}
			return nil
		})
	}
}
