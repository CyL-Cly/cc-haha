package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcptools"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualMCPRichMainPrintNativeLeaseRetainsLateFilesAfterFailFastAndPreservesForeignOwnerGuard(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := io.ReadAll(r.Body)
		var input protocol.JSONObject
		input.UnmarshalJSON(p)
		if input.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		var result json.RawMessage
		switch input.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"private-rich-lifetime","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"rich","inputSchema":{"type":"object"}}]}`)
		case "tools/call":
			result = []byte(`{"content":[{"type":"image","data":"","mimeType":"image/png"},{"type":"audio","data":"AAE/Cg==","mimeType":"audio/wav"},{"type":"resource","resource":{"uri":"owned:///late","mimeType":"text/plain","blob":"bGF0ZS1maWxl"}}]}`)
		default:
			http.Error(w, "unowned", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(map[string]any{"mcpServers": map[string]any{"fixture": map[string]string{"type": "http", "url": peer.URL}}})))
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual rich lifetime", Tools: []string{"mcp__fixture__rich"}, PermissionMode: "bypassPermissions", MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	if e = m.Host.WaitTranscriptRuntime(ctx); e != nil {
		t.Fatal("baseline actual native jobs not drained", e)
	}
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	binding, e := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if e != nil {
		t.Fatal(e)
	}
	// Reuse this MainPrint's real Controller ledger/runtime/connections. The
	// seam delays only the submitted native file write, then calls its original
	// check+os.WriteFile; it allocates neither a Harness nor another owner.
	service, e := mcptools.New(mcptools.Options{Owner: m.Owner, NativeBinding: binding, Runtime: m.mcpRuntime, Configurations: m.mcpConfigurations, Environment: f.n.Environment, PermissionContext: func(scope *cooperative.Scope) (protocol.JSONObject, error) {
		_, p, e := m.Controller.SDKSettingsOnOwner(scope)
		return p, e
	}, ScopeForContext: func(value any) *cooperative.Scope {
		if call, ok := value.(*builtinwrite.CallContext); ok {
			return call.Scope
		}
		return nil
	}, ResultOptions: func(scope *cooperative.Scope) (mcpruntime.RichResultOptions, error) {
		options, e := m.nativeMCPResultOptions(scope)
		write := options.WriteFile
		options.WriteFile = func(path string, data []byte) error { started <- struct{}{}; <-release; return write(path, data) }
		return options, e
	}})
	if e != nil {
		t.Fatal(e)
	}
	var tool queryloop.RegisteredTool
	directory := ""
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if e := service.Discover(scope); e != nil {
			return e
		}
		_, tools, e := service.Catalog(scope)
		if e != nil {
			return e
		}
		for _, candidate := range tools {
			if candidate.Name == "mcp__fixture__rich" {
				tool = candidate
			}
		}
		directory, e = m.Controller.ToolResultsDirectoryOnOwner(scope)
		return e
	}); e != nil || tool.StartCallJSON == nil {
		t.Fatal("actual MCP tool not bound", e)
	}
	e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, e := tool.StartCallJSON(ctx, protocol.NewJSONObject(), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
		if e != nil {
			return e
		}
		_, e = future.Await(scope)
		return e
	})
	if e == nil || e.Error() != "Image file is empty (0 bytes)" {
		t.Fatal("actual fail-fast MCP result did not deliver before blocked native writes", e)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("actual sibling native write not started")
		}
	}
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	e = m.Host.WaitTranscriptRuntime(short)
	stop()
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("real Writer native refs were released before Promise.all sibling IO finished", e)
	}
	close(release)
	released = true
	if e = m.Host.WaitTranscriptRuntime(ctx); e != nil {
		t.Fatal("all-settled native refs not released on actual owner", e)
	}
	paths, e := filepath.Glob(filepath.Join(directory, "mcp-fixture-blob-*"))
	if e != nil || len(paths) != 2 {
		t.Fatal("late physical files were lost after image rejection", paths, e)
	}
	for _, path := range paths {
		p, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasSuffix(path, ".txt") && string(p) != "late-file" {
			t.Fatal("late binary bytes changed")
		}
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if e = foreign.Run(ctx, func(scope *cooperative.Scope) error { _, e := m.nativeMCPResultOptions(scope); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("foreign owner snapshot admitted", e)
	}
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal("actual shared shutdown did not join rich results", e)
	}
}
