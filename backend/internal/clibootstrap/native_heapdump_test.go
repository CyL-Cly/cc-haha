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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/heapdump"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/google/pprof/profile"
)

func TestActualHeapDumpLocalZeroPOSTPhysicalProfileCanonicalAndNextHarness(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		emitWorktreeModel(w, 1, "", "", "HEAP_NATIVE_NEXT_HARNESS_DONE_921")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual native heap command", Tools: []string{"Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "heap-version", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
	}
	if err = m.RunTurn(ctx, user("/heapdump ignored args")); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if calls.Load() != 0 {
		t.Fatal("heap profile invoked model")
	}
	sid := m.Controller.SessionID()
	path := filepath.Join(f.home, "Desktop", sid+".heap.pprof")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = profile.ParseData(data); err != nil {
		t.Fatal("real CLI produced unreadable profile", err)
	}
	diagPath := filepath.Join(f.home, "Desktop", sid+"-diagnostics.json")
	raw, err := os.ReadFile(diagPath)
	if err != nil {
		t.Fatal(err)
	}
	var diag heapdump.Diagnostics
	if err = json.Unmarshal(raw, &diag); err != nil || diag.SessionID != sid || diag.Runtime != "go" || diag.CCVersion != "heap-version" || diag.MemoryUsage.HeapAlloc == 0 {
		t.Fatal("wrong native session/runtime diagnostics", diag, err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		for _, command := range m.Registry.ControlCommands() {
			if command.Name == "heapdump" {
				if !command.Hidden {
					return io.ErrUnexpectedEOF
				}
				return nil
			}
		}
		return io.ErrUnexpectedEOF
	}); err != nil {
		t.Fatal("original hidden command marker absent", err)
	}
	if err = m.RunTurn(ctx, user("Continue after actual heap dump.")); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if calls.Load() != 1 {
		t.Fatal("next shared Harness", calls.Load())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	canonical, err := os.ReadFile(files[0])
	if err != nil || !bytes.Contains(canonical, []byte("<command-name>/heapdump")) || !bytes.Contains(canonical, []byte(sid+".heap.pprof")) || !bytes.Contains(canonical, []byte("HEAP_NATIVE_NEXT_HARNESS_DONE_921")) {
		t.Fatal("physical local command/results missing", err)
	}
}
