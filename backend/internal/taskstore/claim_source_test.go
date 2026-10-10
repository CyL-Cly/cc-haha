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
	"sync"
	"testing"
)

func TestActualTaskClaimOriginalTransactionsAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	base := func(extra string) string {
		return `{"subject":"actual","description":"full task","status":"pending","blocks":[],"blockedBy":[]` + extra + `}`
	}
	rows := []map[string]any{}
	add := func(tasks []string, id string, busy, mark bool) {
		values := []any{}
		for _, task := range tasks {
			values = append(values, taskInput(task))
		}
		rows = append(rows, map[string]any{"tasks": values, "id": id, "claimant": "worker", "options": map[string]bool{"checkAgentBusy": busy, "markInProgress": mark}})
	}
	add(nil, "missing", false, false)
	add([]string{base(`,"owner":"other"`)}, "1", false, true)
	add([]string{`{"subject":"complete","description":"full","status":"completed","blocks":[],"blockedBy":[]}`}, "1", true, true)
	add([]string{`{"subject":"blocked","description":"full","status":"pending","blocks":[],"blockedBy":["2"]}`, base("")}, "1", false, true)
	add([]string{base(""), base(`,"owner":"worker"`)}, "1", true, true)
	add([]string{`{"subject":"same","description":"full","status":"in_progress","blocks":[],"blockedBy":[],"owner":"worker"}`}, "1", true, true)
	add([]string{base(""), base(`,"owner":"worker"`)}, "1", false, true)
	add([]string{base(""), `{"subject":"complete","description":"full","status":"completed","blocks":[],"blockedBy":[],"owner":"worker"}`}, "1", true, true)
	add([]string{base(`,"owner":"worker"`)}, "1", false, false)
	add([]string{base("")}, "1", true, false)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	goHome := t.TempDir()
	store := testStore(t, filepath.Join(goHome, ".claude"))
	actual := []map[string]any{}
	for i, row := range rows {
		list := fmt.Sprintf("actual-claim-%d", i)
		for _, task := range row["tasks"].([]any) {
			if _, err := store.Create(list, task.(protocol.JSONObject)); err != nil {
				t.Fatal(err)
			}
		}
		options := row["options"].(map[string]bool)
		result, err := store.Claim(list, row["id"].(string), "worker", ClaimOptions{options["checkAgentBusy"], options["markInProgress"]})
		var errorText any
		if err != nil {
			errorText = err.Error()
		}
		snapshot, err := store.Snapshot(list)
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, map[string]any{"result": result, "error": errorText, "tasks": snapshot.Tasks, "revision": snapshot.Revision})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-task-claim.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_CLAIM_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var report struct {
		Rows         json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &report); err != nil || len(report.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	var a, b any
	want := raw(actual)
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal(report.Rows, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go %s\nSource %s", want, report.Rows)
	}
	assertNoLeases(t, goHome)
}

func TestConcurrentNativeClaimUsesOriginalSharedLifecycleAndListLeases(t *testing.T) {
	home := t.TempDir()
	a, b := testStore(t, home), testStore(t, home)
	if _, err := a.Create("team", baseTask("one actual task")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan bool, 2)
	fail := make(chan error, 2)
	for i, store := range []*Service{a, b} {
		wg.Add(1)
		go func(i int, store *Service) {
			defer wg.Done()
			result, err := store.Claim("team", "1", fmt.Sprintf("worker-%d", i), ClaimOptions{true, true})
			out <- string(result.Get("success")) == "true"
			fail <- err
		}(i, store)
	}
	wg.Wait()
	close(out)
	close(fail)
	claimed := 0
	for success := range out {
		if success {
			claimed++
		}
	}
	for err := range fail {
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := a.Snapshot("team")
	if err != nil || claimed != 1 || snapshot.Revision != 2 {
		t.Fatal("claim raced", claimed, snapshot.Revision, err)
	}
	assertNoLeases(t, home)
}
