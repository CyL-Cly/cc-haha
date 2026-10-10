package taskstore

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTeamLifecycleGenerationTerminalAndLegacyStateMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	goHome := t.TempDir()
	store := testStore(t, filepath.Join(goHome, ".claude"))
	identity := map[string]any{"teamName": "team", "createdAt": 1000, "leadSessionId": "parent"}
	steps := []map[string]any{{"action": "read"}, {"seed": map[string]any{"schemaVersion": 1, "generation": 0, "deleted": false, "terminals": []any{}, "future": map[string]bool{"preserve": true}}}, {"action": "begin", "identity": identity, "token": "first"}, {"action": "read"}, {"action": "complete", "token": "first", "snapshot": map[string]any{"tasks": []any{}, "revision": 7, "capturedAt": "2026-10-07T01:00:00.000Z"}}, {"action": "complete", "token": "first", "snapshot": map[string]any{"tasks": []any{}, "revision": 99, "capturedAt": "later"}}, {"action": "create"}, {"action": "begin", "identity": map[string]any{"teamName": "team", "createdAt": 2000, "leadSessionId": "other-parent"}, "token": "next"}, {"action": "complete", "token": "first", "snapshot": map[string]any{"tasks": []any{}, "revision": 7, "capturedAt": "2026-10-07T01:00:00.000Z"}}, {"action": "read"}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-lifecycle-state.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_LIFECYCLE_STATE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "steps": steps}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(steps) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	tokens := map[string]LifecycleToken{}
	for i, step := range steps {
		var value any
		var failure error
		if seed := step["seed"]; seed != nil {
			path := store.lifecyclePath("team", ".state.json")
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			failure = os.WriteFile(path, raw(seed), 0600)
		} else {
			switch step["action"] {
			case "read":
				var got protocol.JSONObject
				got, failure = store.ReadLifecycle("team")
				if failure == nil {
					value = got
				}
			case "begin":
				var id protocol.JSONObject
				_ = id.UnmarshalJSON(raw(step["identity"]))
				token, err := store.BeginLifecycle("team", id)
				failure = err
				if err == nil {
					tokens[step["token"].(string)] = token
					value = map[string]any{"generation": token.Generation, "identity": token.Identity}
				}
			case "complete":
				var snapshot protocol.JSONObject
				_ = snapshot.UnmarshalJSON(raw(step["snapshot"]))
				var got protocol.JSONObject
				got, failure = store.CompleteLifecycleJSON("team", snapshot, tokens[step["token"].(string)])
				if failure == nil {
					value = got
				}
			case "create":
				creation, err := store.Create("team", baseTask("blocked stale writer"))
				failure = err
				if err == nil {
					value = creation.TaskID
				}
			}
		}
		var errorText, file any
		if failure != nil {
			errorText = failure.Error()
		}
		if content, err := os.ReadFile(store.lifecyclePath("team", ".state.json")); err == nil {
			file = string(content)
		}
		actual := raw(map[string]any{"value": value, "error": errorText, "file": file})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("step %d Go %s Source %s", i, actual, source.Rows[i])
		}
	}
}
