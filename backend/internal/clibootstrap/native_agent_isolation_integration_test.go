package clibootstrap

import (
	"bytes"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualDefaultAgentIsolationRunsConfiguredWorktreeCreateProcess(t *testing.T) {
	f := worktreeMainFixture(t)
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-isolation-606"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	tree := filepath.Join(f.home, "actual-hook-tree")
	journal := filepath.Join(f.home, "hook-journal.json")
	hook := filepath.Join(f.home, "actual-create.py")
	body := "import sys,json,os\ninp=json.load(sys.stdin)\njson.dump(inp,open(" + string(quoteMain(journal)) + ",'w'))\nos.makedirs(" + string(quoteMain(tree)) + ",exist_ok=True)\nopen(os.path.join(" + string(quoteMain(tree)) + ",'hook-owned.txt'),'w').write('ACTUAL_HOOK_ISOLATION_606')\nprint(" + string(quoteMain(tree)) + ")\n"
	f.write(t, hook, body)
	settings := map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "hooks": map[string]any{"WorktreeCreate": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + hook}}}}}}
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
	var mu sync.Mutex
	requests := [][]byte{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, raw)
		round := len(requests)
		mu.Unlock()
		switch round {
		case 1:
			emitWorktreeModel(w, round, "Agent", `{"prompt":"Read hook-owned.txt and report its actual content","description":"actual hook isolated reader","subagent_type":"general-purpose","isolation":"worktree"}`, "")
		case 2:
			emitWorktreeModel(w, round, "Read", `{"file_path":"hook-owned.txt"}`, "")
		case 3:
			emitWorktreeModel(w, round, "", "", "actual isolated child complete")
		case 4:
			emitWorktreeModel(w, round, "", "", "actual parent complete")
		default:
			t.Errorf("unexpected physical model request %d", round)
			http.Error(w, "unexpected request", 500)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	var output, diagnostics bytes.Buffer
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, SessionID: "60600000-0000-4000-8000-000000000606", Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: &output, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := m.state.Snapshot()
	input := protocol.NewJSONObject()
	_ = input.UnmarshalJSON([]byte(`{"type":"user","uuid":"actual-hook-agent-606","message":{"role":"user","content":"Run one isolated reader through the configured WorktreeCreate hook"}}`))
	if err = m.RunTurn(context.Background(), input); err != nil {
		t.Fatal("actual default Agent", err, diagnostics.String())
	}
	if err = m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 4 {
		t.Fatalf("configured WorktreeCreate must reach actual child: requests=%d diagnostics=%s output=%s", len(requests), diagnostics.String(), output.String())
	}
	raw, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal("actual WorktreeCreate process did not execute", err)
	}
	var event protocol.JSONObject
	if err = event.UnmarshalJSON(raw); err != nil {
		t.Fatal(err)
	}
	if event.String("hook_event_name") != "WorktreeCreate" || event.String("session_id") != original.SessionID || !strings.HasPrefix(event.String("name"), "agent-") {
		t.Fatal("actual hook identity", string(raw))
	}
	if !bytes.Contains(requests[1], []byte("Working directory: "+tree)) || !bytes.Contains(requests[2], []byte("ACTUAL_HOOK_ISOLATION_606")) {
		t.Fatal("actual child worktree prompt/read consumer")
	}
	if current := m.state.Snapshot(); current.CWD != original.CWD || current.OriginalCWD != original.OriginalCWD || current.ProjectRoot != original.ProjectRoot {
		t.Fatal("Agent isolation retargeted parent")
	}
	paths, err := filepath.Glob(filepath.Join(f.config, "projects", "*", original.SessionID, "subagents", "agent-*.meta.json"))
	if err != nil || len(paths) != 1 {
		t.Fatal("child metadata", paths, err)
	}
	metadata, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]json.RawMessage
	if err = json.Unmarshal(metadata, &row); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(row["worktreePath"], quoteMain(tree)) {
		t.Fatal("hook-based worktree metadata", string(metadata))
	}
	if _, err = os.Stat(tree); err != nil {
		t.Fatal("Source retains hook-based Agent worktree", err)
	}
}
