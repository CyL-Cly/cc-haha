package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
)

// Exact bytes of the authorized saved P868 workflow; no user profile reads at test time.
const actualSDKSlashSavedWorkflowP868 = `export const meta = { name: 'native-p868-readers', description: 'Live completion save verification', phases: [{ title: 'Read', detail: 'Read two physical inputs' }] };
phase('Read');
const results = await parallel([() => agent('实际用 Read 读取当前目录 native-workflow-input-P868-a.txt，报告完整第一行 marker 和第二行整数之和；不得修改文件。', {label: 'Reader A'}), () => agent('实际用 Read 读取当前目录 native-workflow-input-P868-b.txt，报告完整第一行 marker 和第二行整数之和；不得修改文件。', {label: 'Reader B'})]);
log(JSON.stringify(results));`

func TestActualDesktopFactorySavedWorkflowContentBlockSlashInput(t *testing.T) {
	var mu sync.Mutex
	requests := [][]byte{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		response(w, "actual-desktop-slash", "", nil, "ACTUAL_DESKTOP_SLASH_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = []string{"Workflow,Skill,Read,Write,Agent,TaskOutput"}
	o.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	o.Environment["CLAUDE_CODE_TMPDIR"] = filepath.Join(o.Environment["HOME"], "private-task-tmp")
	o.Native.Environment["CLAUDE_CODE_TMPDIR"] = o.Environment["CLAUDE_CODE_TMPDIR"]
	if err := os.MkdirAll(filepath.Join(o.WorkDir, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(o.WorkDir, ".claude", "workflows")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	script := []byte(actualSDKSlashSavedWorkflowP868)
	var err error
	if err = os.WriteFile(filepath.Join(folder, "native-p868-readers.js"), script, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	frames := [][]byte{}
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir, PermissionMode: "default"}, func(data json.RawMessage) {
		mu.Lock()
		frames = append(frames, append([]byte(nil), data...))
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(context.Background())
	// This is the original renderer frame. DesktopFactory's real attachment builder
	// produces SDK text content blocks; the regression must keep that producer.
	if err = live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "/native-p868-readers", "attachments": []any{}})); err != nil {
		t.Fatalf("actual DesktopFactory default raw-block slash failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 || !strings.Contains(string(requests[0]), "Run the saved workflow") {
		t.Fatal("actual desktop slash never expanded saved Workflow", len(requests))
	}
	for _, frame := range frames {
		if obj(frame).String("type") == "error" {
			t.Fatal("desktop emitted error", string(frame))
		}
	}
}

func TestActualDesktopFactorySavedWorkflowSlashRunsOriginalTwoReadersAndCloses(t *testing.T) {
	var mu sync.Mutex
	parentPosts, childPosts := 0, map[string]int{}
	var work string
	taskID := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "workflow orchestration script") {
			label := "a"
			if strings.Contains(string(body), "native-workflow-input-P868-b.txt") {
				label = "b"
			}
			mu.Lock()
			childPosts[label]++
			round := childPosts[label]
			mu.Unlock()
			if round == 1 {
				response(w, "actual-reader-"+label, "Read", map[string]any{"file_path": filepath.Join(work, "native-workflow-input-P868-"+label+".txt")}, "")
				return
			}
			if !strings.Contains(string(body), "ACTUAL_P869_MARKER_"+label) {
				t.Error("real child lost physical Read result", label)
			}
			response(w, "actual-reader-done-"+label, "", nil, "ACTUAL_P869_MARKER_"+label+" integer sum verified")
			return
		}
		mu.Lock()
		parentPosts++
		round := parentPosts
		mu.Unlock()
		switch round {
		case 1:
			if !strings.Contains(string(body), "Run the saved workflow") {
				t.Error("parent did not receive expanded real saved command")
			}
			response(w, "actual-workflow-start", "Workflow", map[string]any{"name": "native-p868-readers"}, "")
		case 2:
			var request struct{ Messages []protocol.JSONObject }
			_ = json.Unmarshal(body, &request)
			for _, message := range request.Messages {
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(message.Get("content"), &blocks)
				for _, block := range blocks {
					if block.String("type") == "tool_result" {
						var launched protocol.JSONObject
						if launched.UnmarshalJSON([]byte(block.String("content"))) == nil && launched.String("status") == "async_launched" {
							taskID = launched.String("taskId")
						}
					}
				}
			}
			if taskID == "" {
				t.Error("actual saved Workflow did not launch")
			}
			response(w, "actual-workflow-output", "TaskOutput", map[string]any{"task_id": taskID, "block": true, "timeout": 30000}, "")
		default:
			if !strings.Contains(string(body), "<retrieval_status>success</retrieval_status>") {
				t.Error("parent did not consume actual TaskOutput")
			}
			response(w, "actual-workflow-parent-done", "", nil, "ACTUAL_SAVED_WORKFLOW_DONE")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	work = o.WorkDir
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = []string{"Workflow,Skill,Read,Write,Agent,TaskOutput"}
	o.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	o.Environment["CLAUDE_CODE_TMPDIR"] = filepath.Join(o.Environment["HOME"], "private-task-tmp")
	o.Native.Environment["CLAUDE_CODE_TMPDIR"] = o.Environment["CLAUDE_CODE_TMPDIR"]
	folder := filepath.Join(work, ".claude", "workflows")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	script := []byte(actualSDKSlashSavedWorkflowP868)
	var err error
	if err = os.WriteFile(filepath.Join(folder, "native-p868-readers.js"), script, 0600); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"a", "b"} {
		if err = os.WriteFile(filepath.Join(work, "native-workflow-input-P868-"+label+".txt"), []byte("ACTUAL_P869_MARKER_"+label+"\n17 29\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	frames := []protocol.JSONObject{}
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: work, PermissionMode: "default"}, func(data json.RawMessage) { mu.Lock(); frames = append(frames, obj(data)); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(context.Background())
	if err = live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "/native-p868-readers", "attachments": []any{}})); err != nil {
		t.Fatal("actual saved workflow desktop slash", err)
	}
	if err = live.Close(ctx); err != nil {
		t.Fatal("real desktop Close", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if childPosts["a"] != 2 || childPosts["b"] != 2 || parentPosts < 3 || taskID == "" {
		t.Fatal("original two-reader Workflow not executed", childPosts, parentPosts, taskID)
	}
	completed := 0
	for _, frame := range frames {
		if frame.String("type") == "error" {
			t.Fatal("desktop error", string(raw(frame)))
		}
		if frame.String("type") == "system_notification" && frame.String("subtype") == "task_notification" {
			data, _ := frame.Object("data")
			if data.String("task_id") == taskID && data.String("status") == "completed" && data.String("workflow_run_id") != "" {
				completed++
			}
		}
	}
	if completed != 1 {
		t.Fatal("actual desktop completed workflow bookend lost/duplicated", completed, string(raw(frames)))
	}
}
