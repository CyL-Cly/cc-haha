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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeAutoDreamForkWritesMemoryWithoutParentHistoryOrTranscript(t *testing.T) {
	var mu sync.Mutex
	var requests []json.RawMessage
	var rootRounds, dreamRounds int
	var memory, outside string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, append(json.RawMessage(nil), body...))
		dream := bytes.Contains(body, []byte("# Dream: Memory Consolidation"))
		if dream {
			dreamRounds++
		} else {
			rootRounds++
		}
		round := dreamRounds
		mu.Unlock()
		if !dream {
			emitPlanFixture(w, 100, "", "PARENT_ACTUAL_DONE")
			return
		}
		switch round {
		case 1:
			emitNativeTaskTool(w, "dream-memory-write", "Write", map[string]any{"file_path": filepath.Join(memory, "durable.md"), "content": "ACTUAL_DREAM_MEMORY"})
		case 2:
			emitNativeTaskTool(w, "dream-denied-write", "Write", map[string]any{"file_path": outside, "content": "MUST_NOT_WRITE"})
		case 3:
			emitNativeTaskTool(w, "dream-denied-bash", "Bash", map[string]any{"command": "printf SHOULD_NOT_EXECUTE > '" + outside + "'"})
		default:
			emitPlanFixture(w, 104, "", "DREAM_ACTUAL_COMPLETE")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"autoDreamEnabled":true,"enabledPlugins":{"sample@mkt":false}}`), 0600); err != nil {
			t.Fatal(err)
		}
		project := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(n.CWD))
		if err := os.MkdirAll(project, 0700); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			if err := os.WriteFile(filepath.Join(project, fmt.Sprintf("11111111-1111-4111-8111-%012d.jsonl", i)), []byte(`{"type":"user","message":{"role":"user","content":"private prior session"}}`+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Write,Read,Bash"}; o.Interactive = true })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.Owner.Run(ctx, func(*cooperative.Scope) error {
		var err error
		memory, err = m.memoryPaths.Directory()
		outside = filepath.Join(m.workingDirectory(), "outside-memory.txt")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "PARENT_ACTUAL_PROMPT"})})); err != nil {
		t.Fatal(err)
	}
	var final protocol.JSONObject
	for {
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			rows, err := m.Controller.BackgroundTasksOnOwner(scope)
			for _, row := range rows {
				if row.String("type") == "dream" {
					final = row
				}
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if final.String("status") != "" && final.String("status") != "running" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual dream never completed", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if final.String("status") != "completed" {
		t.Fatal("actual dream terminal", string(rawMain(final)))
	}
	content, err := os.ReadFile(filepath.Join(memory, "durable.md"))
	if err != nil || string(content) != "ACTUAL_DREAM_MEMORY" {
		t.Fatal("memory Write not executed", err, string(content))
	}
	if _, err = os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("fork executed outside-memory mutation", err)
	}
	var history string
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		messages, err := m.Host.OriginalHistoryOnOwner(scope)
		for _, message := range messages {
			data, _ := message.MarshalJSON()
			history += string(data)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history, "PARENT_ACTUAL_PROMPT") || !strings.Contains(history, "PARENT_ACTUAL_DONE") || strings.Contains(history, "DREAM_ACTUAL") || strings.Contains(history, "Memory Consolidation") {
		t.Fatal("fork contaminated actual parent history")
	}
	files, err := filepath.Glob(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", "*", "subagents", "*.jsonl"))
	if err != nil || len(files) != 0 {
		t.Fatal("skipTranscript created Agent sidechain", files, err)
	}
	lock, err := os.ReadFile(filepath.Join(memory, ".consolidate-lock"))
	if err != nil || len(lock) != 0 {
		t.Fatal("successful dream did not complete its lease", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if rootRounds != 1 || dreamRounds != 4 {
		t.Fatal("unexpected real producer request count", rootRounds, dreamRounds)
	}
	for _, request := range requests[1:] {
		if !bytes.Contains(request, []byte("PARENT_ACTUAL_PROMPT")) || !bytes.Contains(request, []byte("PARENT_ACTUAL_DONE")) {
			t.Fatal("fork lost actual cache-safe parent prefix")
		}
	}
}
