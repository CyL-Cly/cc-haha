package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// These are actual initialize -> AgentTool -> independent native child -> real
// 30-second summary -> task_progress consumers. Source oracle separately proves
// truthy malformed values with the same real timer/query/deny chain.
func TestActualSDKAgentSummariesForegroundAndBackgroundUseDiskDenyAndTaskProgress(t *testing.T) {
	for _, background := range []bool{false, true} {
		name := "foreground"
		if background {
			name = "background"
		}
		t.Run(name, func(t *testing.T) { runActualSDKAgentSummary(t, background, false) })
	}
}

func TestActualSDKAgentSummaryTaskStopAbortsAndJoinsBothPhysicalHTTPRequests(t *testing.T) {
	runActualSDKAgentSummary(t, true, true)
}

func runActualSDKAgentSummary(t *testing.T, background, cancelSummary bool) {
	t.Helper()
	f := newNativePluginFixture(t)
	// Native IPC uses the Root-isolated short temporary primitive, rather than
	// embedding this long test name in a Unix socket address. HOME stays private.
	shortTemp, err := os.MkdirTemp(os.TempDir(), "sum-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(shortTemp); err != nil {
			t.Error(err)
		}
	})
	f.n.Temp = shortTemp
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	for key, value := range map[string]string{"ANTHROPIC_API_KEY": "fake-agent-summary", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_DISABLE_ATTACHMENTS": "1", "USER_TYPE": "external", "PATH": "/usr/bin:/bin", "CLAUDE_CODE_TMPDIR": f.n.Temp} {
		f.n.Environment[key] = value
	}
	delete(f.n.Environment, "CLAUDE_AUTO_BACKGROUND_TASKS")
	written, forbidden := filepath.Join(f.cwd, "agent-actual.txt"), filepath.Join(f.cwd, "summary-forbidden.txt")
	expectedSummaries := 1
	if !background && !cancelSummary {
		expectedSummaries = 2
	}
	failFirstSummary := background && !cancelSummary
	released := make(chan struct{})
	var release sync.Once
	finishChild := func() { release.Do(func() { close(released) }) }
	t.Cleanup(finishChild)
	summaryOpened, summaryClosed, childClosed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var summaryOpenOnce, summaryCloseOnce, childCloseOnce sync.Once
	var mu sync.Mutex
	var childRequests, summaryRequests, parentRequests []protocol.JSONObject
	var frames []protocol.JSONObject
	var childBodies, summaryBodies []string
	var taskID string
	publishedSummaries := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request protocol.JSONObject
		if err = request.UnmarshalJSON(data); err != nil {
			t.Error(err)
			return
		}
		isSummary := bytes.Contains(data, []byte("Describe your most recent action in 3-5 words"))
		isParent := strings.Contains(string(request.Get("system")), "SDK_PARENT_SUMMARY_PREFIX")
		mu.Lock()
		var n int
		if isSummary {
			summaryRequests = append(summaryRequests, request)
			summaryBodies = append(summaryBodies, string(data))
			n = len(summaryRequests)
		} else if isParent {
			parentRequests = append(parentRequests, request)
			n = len(parentRequests)
		} else {
			childRequests = append(childRequests, request)
			childBodies = append(childBodies, string(data))
			n = len(childRequests)
		}
		mu.Unlock()
		if isSummary {
			summaryOpenOnce.Do(func() { close(summaryOpened) })
			if cancelSummary {
				<-r.Context().Done()
				summaryCloseOnce.Do(func() { close(summaryClosed) })
				return
			}
			if failFirstSummary && n == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"private summary transient"}}`)
				return
			}
			summaryRound := n
			if failFirstSummary {
				summaryRound--
			}
			if summaryRound == 1 {
				emitNativeTaskTool(w, "summary-must-deny", "Write", map[string]any{"file_path": forbidden, "content": "MUST_NOT_WRITE"})
				return
			}
			if summaryRound == 2 {
				if !bytes.Contains(data, []byte("No tools needed for summary")) {
					t.Error("summary did not consume actual Write denial")
				}
				emitPlanFixture(w, n, "", " Reading summary-target.ts ")
				return
			}
			if !sdkAgentSummaryHasPrevious(request) {
				t.Error("next summary did not consume exact previous summary")
			}
			emitPlanFixture(w, n, "", " Fixing summary-target.ts ")
			return
		}
		if isParent {
			if n == 1 {
				emitNativeTaskTool(w, "actual-summary-agent", "Agent", map[string]any{"description": "Summarize the real child", "prompt": "Write then read the physical file and finish", "run_in_background": background})
				return
			}
			emitPlanFixture(w, n, "", "PARENT_SUMMARY_COMPLETE")
			return
		}
		switch n {
		case 1:
			emitNativeTaskTool(w, "actual-child-write", "Write", map[string]any{"file_path": written, "content": "ACTUAL_CHILD_DISK_CONTEXT"})
		case 2:
			emitNativeTaskTool(w, "actual-child-read", "Read", map[string]any{"file_path": written})
		default:
			select {
			case <-released:
				emitPlanFixture(w, n, "", "CHILD_SUMMARY_COMPLETE")
			case <-r.Context().Done():
				childCloseOnce.Do(func() { close(childClosed) })
			}
		}
	}))
	t.Cleanup(peer.Close)
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	ctx, cancel := context.WithTimeout(context.Background(), 85*time.Second)
	defer cancel()
	disabled := false
	init := `{"type":"control_request","request_id":"summary-init","request":{"subtype":"initialize","agentProgressSummaries":true}}`
	user := `{"type":"user","message":{"role":"user","content":"Launch the actual Agent"}}`
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "SDK_PARENT_SUMMARY_PREFIX", Tools: []string{"Agent,Write,Read"}, MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(init + "\n" + user + "\n"), Output: io.Discard, Error: &diagnostics, OnOutput: func(data []byte) error {
		var frame protocol.JSONObject
		if err := frame.UnmarshalJSON(data); err != nil {
			return err
		}
		mu.Lock()
		frames = append(frames, frame)
		if frame.String("subtype") == "task_started" && frame.String("task_type") == "local_agent" {
			taskID = frame.String("task_id")
		}
		isSummary := frame.String("subtype") == "task_progress" && frame.Get("summary") != nil
		if isSummary {
			publishedSummaries++
		}
		completeSummaries := publishedSummaries >= expectedSummaries
		mu.Unlock()
		if isSummary && completeSummaries {
			finishChild()
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.ShutdownSession(context.Background()); err != nil {
			t.Error(err)
		}
	})
	done := make(chan error, 1)
	go func() {
		code, err := m.Run(ctx)
		if err == nil && code != 0 {
			err = fmt.Errorf("actual SDK exit %d", code)
		}
		done <- err
	}()
	if cancelSummary {
		select {
		case <-summaryOpened:
		case err := <-done:
			t.Fatal("SDK exited before actual summary HTTP", err, diagnostics.String())
		case <-ctx.Done():
			t.Fatal(ctx.Err(), diagnostics.String())
		}
		mu.Lock()
		id := taskID
		mu.Unlock()
		if id == "" {
			t.Fatal("actual Agent task was not registered")
		}
		if _, err = m.StopBackgroundTask(ctx, id); err != nil {
			t.Fatal("actual task Stop", err)
		}
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal("actual initialize/Agent/summary", err, diagnostics.String())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err(), diagnostics.String())
	}
	mu.Lock()
	actualFrames := append([]protocol.JSONObject{}, frames...)
	children := append([]protocol.JSONObject{}, childRequests...)
	summaries := append([]protocol.JSONObject{}, summaryRequests...)
	id := taskID
	actualChildBodies := append([]string{}, childBodies...)
	actualSummaryBodies := append([]string{}, summaryBodies...)
	mu.Unlock()
	if cancelSummary {
		if len(summaries) != 1 {
			t.Fatal("actual cancellation query count", len(summaries))
		}
		// No second cleanup call is allowed to supply the cancellation/join proof.
		select {
		case <-summaryClosed:
		case <-ctx.Done():
			t.Fatal("actual summary HTTP did not close after task Stop", ctx.Err())
		}
		select {
		case <-childClosed:
		case <-ctx.Done():
			t.Fatal("actual child HTTP did not close after task Stop", ctx.Err())
		}
		for _, frame := range actualFrames {
			if frame.Get("summary") != nil && frame.String("subtype") == "task_progress" {
				t.Fatal("stopped summary published", string(rawMain(frame)))
			}
		}
		t.Logf("actual_agent_summary_cancel_evidence=%s", rawMain(map[string]any{"taskID": id, "summaryRequests": len(summaries), "childRequests": len(children), "summaryPhysicalClosed": true, "childPhysicalClosed": true, "frames": actualFrames}))
		return
	}
	expectedRequests := expectedSummaries + 1
	if failFirstSummary {
		expectedRequests++
	}
	if len(children) != 3 || len(summaries) != expectedRequests || id == "" {
		t.Fatal("actual producer cardinality", len(children), len(summaries), id, diagnostics.String())
	}
	count := 0
	for _, frame := range actualFrames {
		if frame.String("subtype") != "task_progress" || frame.Get("summary") == nil {
			continue
		}
		count++
		expectedText := "Reading summary-target.ts"
		if count == 2 {
			expectedText = "Fixing summary-target.ts"
		}
		if frame.String("task_id") != id || frame.String("tool_use_id") != "actual-summary-agent" || frame.String("summary") != expectedText || frame.String("description") != expectedText {
			t.Fatal("actual summary SDK shape", string(rawMain(frame)))
		}
		usage, _ := frame.Object("usage")
		if string(usage.Get("tool_uses")) != "2" {
			t.Fatal("summary lost actual agent progress", string(rawMain(frame)))
		}
	}
	if count != expectedSummaries {
		t.Fatal("summary publication", count, expectedSummaries)
	}
	t.Logf("actual_agent_summary_public_evidence=%s", rawMain(map[string]any{"background": background, "firstSummaryHTTP500": failFirstSummary, "actualSummaries": count, "taskID": id, "frames": actualFrames}))
	data, err := os.ReadFile(written)
	if err != nil || string(data) != "ACTUAL_CHILD_DISK_CONTEXT" {
		t.Fatal("actual child did not write/read physical file", string(data), err)
	}
	if _, err = os.Stat(forbidden); !os.IsNotExist(err) {
		t.Fatal("summary bypassed true deny-tools callback", err)
	}
	childSystem, err := sdkAgentSummaryComparableSystem([]byte(actualChildBodies[2]))
	if err != nil {
		t.Fatal(err)
	}
	for index, request := range summaries {
		summarySystem, err := sdkAgentSummaryComparableSystem([]byte(actualSummaryBodies[index]))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(childSystem, summarySystem) {
			t.Fatal("summary changed child system cache fields beyond unique uncached CCH")
		}

		if !bytes.Equal(request.Get("tools"), children[2].Get("tools")) || request.String("model") != children[2].String("model") || !bytes.Equal(request.Get("thinking"), children[2].Get("thinking")) || !bytes.Equal(request.Get("max_tokens"), children[2].Get("max_tokens")) {
			t.Fatal("summary changed child cache-key params")
		}
		if !bytes.Contains(request.Get("messages"), []byte("ACTUAL_CHILD_DISK_CONTEXT")) {
			t.Fatal("summary failed to read actual current disk transcript")
		}
		var messages []protocol.JSONObject
		if err = json.Unmarshal(request.Get("messages"), &messages); err != nil {
			t.Fatal(err)
		}
		var marks []int
		for i, message := range messages {
			if bytes.Contains(rawMain(message), []byte("cache_control")) {
				marks = append(marks, i)
			}
		}
		if len(marks) != 1 || marks[0] != len(messages)-1 {
			t.Fatal("summary must preserve Source cache writes", marks, len(messages))
		}
	}
	verifySDKAgentSummaryBillingWithActualSource(t, ctx, f.home, append([]string{actualChildBodies[2]}, actualSummaryBodies...))
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	files := 0
	err = filepath.WalkDir(filepath.Join(f.config, "projects"), func(path string, d fs.DirEntry, failure error) error {
		if failure != nil {
			return failure
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		files++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, marker := range []string{"Describe your most recent action in 3-5 words", "Reading summary-target.ts", "Fixing summary-target.ts", "summary-must-deny", "MUST_NOT_WRITE"} {
			if bytes.Contains(data, []byte(marker)) {
				return fmt.Errorf("summary contaminated actual transcript %s marker %s", path, marker)
			}
		}
		return nil
	})
	if err != nil || files != 2 {
		t.Fatal("skipTranscript actual root+child transcript boundary", files, err)
	}
}

func sdkAgentSummaryHasPrevious(request protocol.JSONObject) bool {
	var messages []protocol.JSONObject
	if json.Unmarshal(request.Get("messages"), &messages) != nil {
		return false
	}
	for _, message := range messages {
		var blocks []protocol.JSONObject
		if json.Unmarshal(message.Get("content"), &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.String("type") == "text" && strings.Contains(block.String("text"), `Previous: "Reading summary-target.ts" — say something NEW.`) {
				return true
			}
		}
	}
	return false
}
