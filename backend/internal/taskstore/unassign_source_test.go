package taskstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualTeammateUnassignPreservesCompletedForeignAndUnknownTaskFieldsAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	rows := []map[string]any{}
	for _, reason := range []string{"terminated", "shutdown"} {
		for _, tasks := range [][]string{{}, {`{"subject":"physical work","description":"full","status":"in_progress","owner":"worker@team","blocks":[],"blockedBy":[],"metadata":{"preserved":1},"extra":true}`, `{"subject":"name-owned","description":"full","status":"pending","owner":"worker","blocks":[],"blockedBy":[]}`, `{"subject":"complete","description":"full","status":"completed","owner":"worker","blocks":[],"blockedBy":[]}`, `{"subject":"foreign","description":"full","status":"in_progress","owner":"other","blocks":[],"blockedBy":[]}`}} {
			values := []protocol.JSONObject{}
			for _, task := range tasks {
				values = append(values, taskInput(task))
			}
			rows = append(rows, map[string]any{"tasks": values, "id": "worker@team", "name": "worker", "reason": reason})
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	nativeHome := t.TempDir()
	store := testStore(t, nativeHome)
	actual := []map[string]any{}
	for i, row := range rows {
		list := fmt.Sprintf("actual-unassign-%d", i)
		for _, task := range row["tasks"].([]protocol.JSONObject) {
			if _, err := store.Create(list, task); err != nil {
				t.Fatal(err)
			}
		}
		result, err := store.UnassignTeammateTasks(list, row["id"].(string), row["name"].(string), row["reason"].(string))
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.Snapshot(list)
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, map[string]any{"result": result, "tasks": snapshot.Tasks, "revision": snapshot.Revision})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-task-unassign.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_UNASSIGN_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var source struct {
		Rows         json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	var a, b any
	_ = json.Unmarshal(raw(actual), &a)
	_ = json.Unmarshal(source.Rows, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go%s\nSource%s", raw(actual), source.Rows)
	}
	assertNoLeases(t, nativeHome)
}

func TestAtomicTaskUnassignRechecksActualOwnerUnderOriginalLeases(t *testing.T) {
	home := t.TempDir()
	store := testStore(t, home)
	id, err := store.Create("team", taskInput(`{"subject":"real","description":"full","status":"in_progress","owner":"worker","blocks":[],"blockedBy":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateTask("team", quote(id.TaskID), taskInput(`{"owner":"new-owner"}`)); err != nil {
		t.Fatal(err)
	}
	result, err := store.UpdateAtomically("team", quote(id.TaskID), func(current protocol.JSONObject) (*protocol.JSONObject, error) {
		if current.String("owner") != "worker" {
			return nil, nil
		}
		patch := protocol.NewJSONObject(protocol.JSONField{Name: "owner"}, protocol.JSONField{Name: "status", Value: quote("pending")})
		return &patch, nil
	})
	if err != nil || result != nil {
		t.Fatal("conditional transaction changed reassigned work", result, err)
	}
	current, err := store.Get("team", quote(id.TaskID))
	if err != nil || current.String("owner") != "new-owner" || current.String("status") != "in_progress" {
		t.Fatal("actual reassignment lost", current, err)
	}
	assertNoLeases(t, home)
}
