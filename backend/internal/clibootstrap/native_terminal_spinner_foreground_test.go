package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// The full original interactive entrypoint reaches AgentTool.call/runAgent and
// the actual REPL ref setter: one completed child adds 230 UTF-16 units without
// committing the parent. Child streaming events do not drive the parent spinner.
func TestActualForegroundAgentCompletedAssistantAddsParentSpinnerRefBeforeContinuation(t *testing.T) {
	runActualForegroundParentSpinnerCounter(t, "text")
}

func TestActualForegroundAgentCompletedThinkingRedactedAndReadJSONUseParentRefOnly(t *testing.T) {
	for _, profile := range []string{"thinking-redacted", "read-tool"} {
		t.Run(profile, func(t *testing.T) { runActualForegroundParentSpinnerCounter(t, profile) })
	}
}

func runActualForegroundParentSpinnerCounter(t *testing.T, profile string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	childText := "SOURCE_FOREGROUND_CHILD_中文_😀_" + strings.Repeat("x", 200)
	input := map[string]any{"description": "actual foreground response counter", "prompt": "Answer with the configured completion text.", "subagent_type": "general-purpose", "max_turns": 2}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	parentUnits := len(utf16.Encode([]rune(string(inputJSON))))
	childUnits := len(utf16.Encode([]rune(childText)))
	expectedRefs := []int{childUnits}
	expectedChildren := int32(1)
	var readPath string
	var readInput json.RawMessage
	readContent := "SOURCE_CHILD_READ_PHYSICAL_中文_😀"
	if profile == "thinking-redacted" {
		expectedRefs = []int{16, 32, childUnits}
	}
	if profile == "read-tool" {
		expectedChildren = 2
	}
	if parentUnits != 155 || childUnits != 230 {
		t.Fatal("Original full caller fixture changed", parentUnits, childUnits)
	}
	var parents, children atomic.Int32
	parentHeld := make(chan struct{})
	parentRelease := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(parentRelease) }) }
	defer release()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(string(mainObj(body).Get("system")), "private actual parent harness") {
			switch parents.Add(1) {
			case 1:
				emitNativeTaskTool(w, "parent-counter-agent", "Agent", input)
			case 2:
				if !bytes.Contains(body, []byte(childText)) || !bytes.Contains(body, []byte("parent-counter-agent")) {
					t.Error("Actual parent continuation lost original foreground Agent result", string(body))
				}
				close(parentHeld)
				select {
				case <-parentRelease:
					emitPlanFixture(w, 2, "", "FOREGROUND_COUNTER_PARENT_DONE")
				case <-request.Context().Done():
				}
			case 3:
				if !bytes.Contains(body, []byte("FOREGROUND_COUNTER_NEXT_HUMAN")) || !bytes.Contains(body, []byte(childText)) || !bytes.Contains(body, []byte("parent-counter-agent")) {
					t.Error("Next same-Harness human lost actual foreground Agent history", string(body))
				}
				emitPlanFixture(w, 3, "", "FOREGROUND_COUNTER_NEXT_DONE")
			default:
				t.Error("Unexpected actual parent model request", string(body))
			}
			return
		}
		round := children.Add(1)
		if round > expectedChildren {
			t.Error("Unexpected actual foreground child model request", string(body))
		}
		if profile == "read-tool" && round == 1 {
			emitNativeTaskTool(w, "actual-rich-read", "Read", readInput)
			return
		}
		if profile == "read-tool" && !bytes.Contains(body, []byte(readContent)) {
			t.Error("Original actual child Read did not enter its own continuation", string(body))
		}
		if profile == "thinking-redacted" {
			emitForegroundCompletedThinkingFixture(w, childText)
			return
		}
		if !bytes.Contains(body, []byte("Answer with the configured completion text.")) {
			t.Error("Actual foreground child did not receive its real prompt", string(body))
		}
		emitPlanFixture(w, 1, "", childText)
	}))
	defer peer.Close()
	var mu sync.Mutex
	var observations []tui.TerminalSpinnerProgress
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) {
		options.Interactive = true
		options.Arguments.Tools = []string{"Agent"}
		if profile == "read-tool" {
			options.Arguments.Tools = []string{"Agent,Read"}
		}
		options.OnTerminalSpinner = func(progress tui.TerminalSpinnerProgress) error {
			copy := progress
			copy.QueryEvent = append(json.RawMessage(nil), progress.QueryEvent...)
			mu.Lock()
			observations = append(observations, copy)
			mu.Unlock()
			return nil
		}
	})
	defer func() { cancel(); release(); m.Close() }()
	if profile == "read-tool" {
		readPath = filepath.Join(m.options.Native.CWD, "实际_😀_read.txt")
		if err := os.WriteFile(readPath, []byte(readContent+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		pathJSON, _ := json.Marshal(readPath)
		readInput = json.RawMessage(`{"file_path":` + string(pathJSON) + `,"offset":1e0,"limit":1.00000000000000001}`)
		normalized, _ := json.Marshal(map[string]any{"file_path": readPath, "offset": 1, "limit": 1})
		expectedRefs = []int{len(utf16.Encode([]rune(string(normalized)))), childUnits}
	}
	expectedUnits := 0
	for _, value := range expectedRefs {
		expectedUnits += value
	}
	turnDone := make(chan error, 1)
	go func() {
		turnDone <- m.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "Launch the actual foreground agent and report its result."})))
	}()
	select {
	case <-parentHeld:
	case err := <-turnDone:
		t.Fatal("Actual foreground Agent did not reach its held parent continuation", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	mu.Lock()
	units := 0
	var childRefs []int
	for _, progress := range observations {
		units += progress.AddedResponseUnits
		if progress.AddedResponseUnits > 0 && progress.Mode == "" && progress.Verb == nil && len(progress.QueryEvent) == 0 {
			childRefs = append(childRefs, progress.AddedResponseUnits)
		}
		if kind := mainObj(progress.QueryEvent).String("type"); (kind == "assistant" || kind == "stream_event") && bytes.Contains(progress.QueryEvent, []byte(childText)) {
			t.Error("Completed child was exposed as a parent query event instead of a response ref")
		}
	}
	mu.Unlock()
	if units != parentUnits+expectedUnits || !reflect.DeepEqual(childRefs, expectedRefs) || parents.Load() != 2 || children.Load() != expectedChildren {
		t.Fatalf("Completed foreground child missing from actual parent spinner ref: units %d, want %d = parent %d + child %d; ref-only child callbacks %v/%v; HTTP parent %d child %d", units, parentUnits+expectedUnits, parentUnits, expectedUnits, childRefs, expectedRefs, parents.Load(), children.Load())
	}
	release()
	select {
	case err := <-turnDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Original foreground Agent turn did not settle", ctx.Err())
	}
	if err := m.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "FOREGROUND_COUNTER_NEXT_HUMAN"}))); err != nil {
		t.Fatal(err)
	}
	if parents.Load() != 3 || children.Load() != expectedChildren {
		t.Fatal("Actual foreground Agent/next-human HTTP cardinality", parents.Load(), children.Load())
	}
}

// These are original provider events. The actual child query/parser/Agent yields
// the completed records; no finished message or response setter is supplied.
func emitForegroundCompletedThinkingFixture(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	encodedText, _ := json.Marshal(text)
	rows := []json.RawMessage{
		json.RawMessage(`{"type":"message_start","message":{"id":"actual-rich-child","type":"message","role":"assistant","model":"fixture-model","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`),
		json.RawMessage(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`),
		json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"THINKING_中文_😀_\ud800"}}`),
		json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIGNATURE_DOES_NOT_COUNT"}}`),
		json.RawMessage(`{"type":"content_block_stop","index":0}`),
		json.RawMessage(`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"cmVkYWN0ZWRfcHJpdmF0ZV9maXh0dXJl"}}`),
		json.RawMessage(`{"type":"content_block_stop","index":1}`),
		json.RawMessage(`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`),
		json.RawMessage(`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":` + string(encodedText) + `}}`),
		json.RawMessage(`{"type":"content_block_stop","index":2}`),
		json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`),
		json.RawMessage(`{"type":"message_stop"}`),
	}
	for _, row := range rows {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", mainObj(row).String("type"), row)
	}
}
