package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualTaskListEnvironmentChangeFencesOldFiveSecondResetAgainstMountedSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual mounted Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-task-list.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_LIST_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=36"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": []any{map[string]any{"listId": "actual-original-list", "expanded": "none", "steps": []any{
		map[string]any{"toolCreate": map[string]any{"subject": "SOURCE_COMPLETED_OLD_LIST", "description": "Actual completed old task"}},
		map[string]any{"toolUpdate": map[string]any{"taskId": "1", "status": "completed"}},
		map[string]any{"settingEnv": map[string]string{"CLAUDE_CODE_TASK_LIST_ID": "actual-next-list"}},
		map[string]any{"wait": 5300},
	}}}})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Original mounted store/real five-second timer/listID fence", err, string(output))
	}
	var source struct {
		Results [][]struct {
			ListID, ExpandedView string
			Tasks, DiskTasks     []protocol.JSONObject
			CurrentDiskTasks     []protocol.JSONObject
			Global               protocol.JSONObject
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != 1 || len(source.Results[0]) != 5 || len(source.Caps.Blocked) > 0 || len(source.Caps.SecureReads) > 0 {
		t.Fatal(err, string(output))
	}
	created := source.Results[0][1]
	if len(created.Tasks) != 1 || created.Tasks[0].String("status") != "pending" || created.ExpandedView != "tasks" {
		t.Fatal("Original mounted create did not publish a pending task before its completion", string(output))
	}
	want := source.Results[0][4]
	if want.ListID != "actual-next-list" || want.ExpandedView != "tasks" || len(want.Tasks) != 1 || len(want.DiskTasks) != 1 || want.DiskTasks[0].String("status") != "completed" || len(want.CurrentDiskTasks) != 0 {
		t.Fatal("Original stale completion timer changed the old task list or visible cached tasks", string(output))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var calls atomic.Int32
	pendingPublished := make(chan struct{})
	var pendingOnce sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		switch calls.Add(1) {
		case 1:
			emitNativeTaskTool(w, "old-list-create", "TaskCreate", map[string]any{"subject": "SOURCE_COMPLETED_OLD_LIST", "description": "Actual completed old task"})
		case 2:
			// The original mounted fixture observes the pending task after
			// TaskCreate. Hold this real response until the native subscriber
			// reaches that same observable boundary before TaskUpdate.
			select {
			case <-pendingPublished:
			case <-ctx.Done():
				t.Error("Actual created task was not published before completion", ctx.Err())
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
			emitNativeTaskTool(w, "old-list-complete", "TaskUpdate", map[string]any{"taskId": "1", "status": "completed"})
		case 3:
			emitPlanFixture(w, 3, "", "ACTUAL_OLD_LIST_COMPLETED")
		case 4:
			if !bytes.Contains(body, []byte("TASK_ENV_TIMER_NEXT_HUMAN")) || !bytes.Contains(body, []byte("old-list-complete")) {
				t.Error("Actual task timer changed the next same-Harness history", string(body))
			}
			emitPlanFixture(w, 4, "", "ACTUAL_TIMER_NEXT_HUMAN_DONE")
		default:
			t.Error("Unexpected model request", string(body))
		}
	}))
	defer peer.Close()
	var settingsPath string
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_TASK_LIST_ID"] = "actual-original-list"
		settingsPath = filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
	}, func(options *MainPrintOptions) {
		options.Interactive = true
		options.Arguments.Tools = []string{"TaskCreate,TaskUpdate"}
	})
	defer m.Close()
	changed := make(chan protocol.JSONObject, 32)
	_, remove, err := m.SubscribeNativeTerminalTaskList(ctx, func(value json.RawMessage) {
		snapshot := mainObj(value)
		var tasks []protocol.JSONObject
		_ = json.Unmarshal(snapshot.Get("tasks"), &tasks)
		if len(tasks) == 1 && tasks[0].String("subject") == "SOURCE_COMPLETED_OLD_LIST" && tasks[0].String("status") == "pending" {
			pendingOnce.Do(func() { close(pendingPublished) })
		}
		changed <- snapshot
	})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": text}))
	}
	if err = m.RunTurn(ctx, user("Create and complete the old actual task list")); err != nil {
		t.Fatal(err)
	}
	var snapshot protocol.JSONObject
	for {
		select {
		case snapshot = <-changed:
			var tasks []protocol.JSONObject
			_ = json.Unmarshal(snapshot.Get("tasks"), &tasks)
			if len(tasks) == 1 && tasks[0].String("status") == "completed" {
				goto completed
			}
		case <-ctx.Done():
			t.Fatal("Actual completed snapshot was not published", ctx.Err())
		}
	}
completed:
	oldPath := filepath.Join(m.taskStore.Directory("actual-original-list"), "1.json")
	before, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(settingsPath, []byte(`{"env":{"CLAUDE_CODE_TASK_LIST_ID":"actual-next-list"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	waitNativeSettings(t, func() bool {
		id, _ := m.options.Native.nativeEnvironmentValue("CLAUDE_CODE_TASK_LIST_ID")
		return id == "actual-next-list"
	})
	// This waits for the actual native timer; no clock adjustment, state
	// projection or fake scheduler replaces its reset verification.
	timer := time.NewTimer(5300 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case snapshot = <-changed:
		case <-timer.C:
			goto timerFired
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
timerFired:
	after, readErr := os.ReadFile(oldPath)
	var tasks []protocol.JSONObject
	_ = json.Unmarshal(snapshot.Get("tasks"), &tasks)
	if readErr != nil || !bytes.Equal(before, after) || len(tasks) != len(want.Tasks) || tasks[0].String("subject") != want.Tasks[0].String("subject") || tasks[0].String("status") != "completed" || snapshot.String("expandedView") != want.ExpandedView {
		t.Fatal("Actual stale timer changed old file/cache across the Source list ID boundary", readErr, string(rawMain(snapshot)), string(after))
	}
	global, err := m.Global.Get()
	if err != nil || string(global.Get("showExpandedTodos")) != string(want.Global.Get("showExpandedTodos")) {
		t.Fatal("Actual stale timer changed expanded global", err)
	}
	if err = m.RunTurn(ctx, user("TASK_ENV_TIMER_NEXT_HUMAN")); err != nil || calls.Load() != 4 {
		t.Fatal("Actual timer/next-Harness cardinality", calls.Load(), err)
	}
}
