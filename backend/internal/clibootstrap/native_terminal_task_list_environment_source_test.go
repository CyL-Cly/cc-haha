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
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualTaskListSettingsEnvironmentSaveMountedWatcherAndNextToolAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual mounted Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const originalList, nextList = "actual-original-list", "actual-next-list"
	const firstSubject, nextSubject = "ORIGINAL_LIST_TASK", "NEXT_LIST_TASK"
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-task-list.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_LIST_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=36"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": []any{map[string]any{"listId": originalList, "expanded": "none", "width": 100, "height": 36, "steps": []any{
		map[string]any{"toolCreate": map[string]any{"subject": firstSubject, "description": "Actual first task"}},
		map[string]any{"settingEnv": map[string]string{"CLAUDE_CODE_TASK_LIST_ID": nextList}},
		map[string]any{"toolCreate": map[string]any{"subject": nextSubject, "description": "Actual dynamic list task"}},
	}}}})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Original settings writer/env application/TaskCreate/useTasksV2 watcher", err, string(output))
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
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != 1 || len(source.Results[0]) != 4 || len(source.Caps.Blocked) > 0 || len(source.Caps.SecureReads) > 0 {
		t.Fatal(err, string(output))
	}
	frames := source.Results[0]
	subjects := func(tasks []protocol.JSONObject) []string {
		values := make([]string, 0, len(tasks))
		for _, task := range tasks {
			values = append(values, task.String("subject"))
		}
		return values
	}
	if frames[1].ListID != originalList || frames[2].ListID != nextList || !reflect.DeepEqual(subjects(frames[2].Tasks), []string{firstSubject}) || !reflect.DeepEqual(subjects(frames[3].Tasks), []string{nextSubject}) || !reflect.DeepEqual(subjects(frames[3].DiskTasks), []string{firstSubject}) {
		t.Fatal("Original mounted store must retain old cache until the genuine TaskCreate pulse, then rewatch the new list", string(output))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		switch calls.Add(1) {
		case 1:
			emitNativeTaskTool(w, "task-before-env", "TaskCreate", map[string]any{"subject": firstSubject, "description": "Actual first task"})
		case 2:
			emitPlanFixture(w, 2, "", "FIRST_TASK_LIST_DONE")
		case 3:
			emitNativeTaskTool(w, "task-after-env", "TaskCreate", map[string]any{"subject": nextSubject, "description": "Actual dynamic list task"})
		case 4:
			if !bytes.Contains(body, []byte("task-after-env")) || bytes.Contains(body, []byte(`"is_error":true`)) {
				t.Error("Actual post-settings TaskCreate failed", string(body))
			}
			emitPlanFixture(w, 4, "", "NEXT_TASK_LIST_DONE")
		case 5:
			if !bytes.Contains(body, []byte("TASK_LIST_ENV_NEXT_HUMAN")) || !bytes.Contains(body, []byte("task-before-env")) || !bytes.Contains(body, []byte("task-after-env")) {
				t.Error("Task list switch recreated the same-Harness history", string(body))
			}
			emitPlanFixture(w, 5, "", "ACTUAL_TASK_LIST_ENV_HUMAN_DONE")
		default:
			t.Error("Unexpected actual model request", string(body))
		}
	}))
	defer peer.Close()
	var settingsPath string
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_TASK_LIST_ID"] = originalList
		settingsPath = filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
		if err := os.WriteFile(settingsPath, []byte(`{"futureTaskSettings":{"keep":true}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(options *MainPrintOptions) {
		options.Interactive = true
		options.Arguments.Tools = []string{"TaskCreate"}
	})
	defer m.Close()
	actualHost, actualController := m.Host, m.Controller
	changed := make(chan protocol.JSONObject, 64)
	_, remove, err := m.SubscribeNativeTerminalTaskList(ctx, func(value json.RawMessage) { changed <- mainObj(value) })
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	wait := func(list, subject string) protocol.JSONObject {
		t.Helper()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		var latest protocol.JSONObject
		for {
			select {
			case latest = <-changed:
				var tasks []protocol.JSONObject
				_ = json.Unmarshal(latest.Get("tasks"), &tasks)
				if latest.String("listId") == list && reflect.DeepEqual(subjects(tasks), []string{subject}) {
					return latest
				}
			case <-deadline.C:
				t.Errorf("Mounted native store differs from original list %s/task %s: %s", list, subject, rawMain(latest))
				return latest
			}
		}
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": text}))
	}
	if err = m.RunTurn(ctx, user("Create first actual task")); err != nil {
		t.Fatal(err)
	}
	wait(originalList, firstSubject)
	oldTasks, err := m.taskStore.List(originalList)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	saved := mainObj(settings)
	saved.Set("env", rawMain(map[string]string{"CLAUDE_CODE_TASK_LIST_ID": nextList}))
	if err = os.WriteFile(settingsPath, rawMain(saved), 0600); err != nil {
		t.Fatal(err)
	}
	// Genuine settingswatch fs notification applies the real parsed env. The
	// fixture never writes either NativeBoundary.Environment or Controller's map.
	waitNativeSettings(t, func() bool {
		value, _ := m.options.Native.nativeEnvironmentValue("CLAUDE_CODE_TASK_LIST_ID")
		return value == nextList
	})
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		actual, err := m.Controller.TaskListIDOnOwner(scope, "")
		if err == nil && actual != frames[2].ListID {
			t.Errorf("Actual current process env and Controller task-list ID diverged: Source %q Native %q", frames[2].ListID, actual)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = m.RunTurn(ctx, user("Create actual task after settings environment update")); err != nil {
		t.Fatal(err)
	}
	view := wait(nextList, nextSubject)
	nextTasks, err := m.taskStore.List(nextList)
	retained, readErr := m.taskStore.List(originalList)
	if err != nil || readErr != nil || !reflect.DeepEqual(subjects(nextTasks), subjects(frames[3].CurrentDiskTasks)) || !bytes.Equal(rawMain(oldTasks), rawMain(retained)) || view.String("expandedView") != frames[3].ExpandedView {
		t.Error("Actual durable list switch differs from original writer/watcher and preserved old list", string(rawMain(nextTasks)), string(rawMain(retained)), view, err, readErr)
	}
	if err = m.RunTurn(ctx, user("TASK_LIST_ENV_NEXT_HUMAN")); err != nil || calls.Load() != 5 || m.Host != actualHost || m.Controller != actualController {
		t.Fatal("Actual next human reused a different Harness/controller", calls.Load(), err)
	}
	global, err := m.Global.Get()
	settings, readErr = os.ReadFile(settingsPath)
	if err != nil || readErr != nil || string(global.Get("showExpandedTodos")) != string(frames[3].Global.Get("showExpandedTodos")) || string(mainObj(settings).Get("futureTaskSettings")) != `{"keep":true}` {
		t.Fatal("Actual expanded global or unknown settings changed", err, readErr, string(settings))
	}
}
