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
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type clearMCPPhysicalPeer struct {
	server                                *httptest.Server
	initialize, tools, prompts, resources atomic.Int32
	held, release                         chan struct{}
	heldOnce, releaseOnce                 sync.Once
	hold                                  bool
}

func newClearMCPPhysicalPeer(t *testing.T, hold bool) *clearMCPPhysicalPeer {
	t.Helper()
	p := &clearMCPPhysicalPeer{held: make(chan struct{}), release: make(chan struct{}), hold: hold}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/held" {
			select {
			case <-p.held:
				w.WriteHeader(204)
			case <-r.Context().Done():
			}
			return
		}
		if r.URL.Path == "/release" {
			p.releaseOnce.Do(func() { close(p.release) })
			w.WriteHeader(204)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		if e := message.UnmarshalJSON(data); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		var result json.RawMessage
		switch message.String("method") {
		case "initialize":
			p.initialize.Add(1)
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{},"prompts":{},"resources":{}},"serverInfo":{"name":"owned-clear","version":"1"}}`)
		case "tools/list":
			p.tools.Add(1)
			p.heldOnce.Do(func() { close(p.held) })
			if p.hold {
				select {
				case <-p.release:
				case <-r.Context().Done():
					return
				}
			}
			result = []byte(`{"tools":[{"name":"inspect","inputSchema":{"type":"object","properties":{}}}]}`)
		case "resources/list":
			p.resources.Add(1)
			result = []byte(`{"resources":[{"uri":"owned:clear","name":"owned resource"}]}`)
		case "prompts/list":
			p.prompts.Add(1)
			result = []byte(`{"prompts":[{"name":"review","description":"owned clear prompt"}]}`)
		default:
			t.Error("unexpected actual clear MCP wire", message)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	t.Cleanup(func() { p.releaseOnce.Do(func() { close(p.release) }); p.server.Close() })
	return p
}
func TestActualSourceClearMCPEffectsRetainMemoizedPhysicalPeer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	for _, hold := range []bool{false, true} {
		t.Run(map[bool]string{false: "connected", true: "pending-tools"}[hold], func(t *testing.T) {
			f := newNativePluginFixture(t)
			shell := filepath.Join(f.home, "bin", "bash")
			f.write(t, shell, "#!/bin/sh\nexit 0\n")
			if e := os.Chmod(shell, 0700); e != nil {
				t.Fatal(e)
			}
			p := newClearMCPPhysicalPeer(t, hold)
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-clear-mcp.fixture.ts")
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = f.home
			cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_CLEAR_MCP_FIXTURE=1", "COLUMNS=80", "LINES=24", "CI=1", "CLAUDE_CODE_SHELL=" + shell, "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=owned-offline", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "url": p.server.URL, "sid": "11111111-1111-4111-8111-111111111111", "holdList": hold}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, e := cmd.Output()
			if e != nil {
				t.Fatalf("actual Source /clear React effects: %v %.3000s", e, stderr.String())
			}
			var actual struct {
				Before, Reset, After   protocol.JSONObject
				SameClient, SIDChanged bool
			}
			if e = json.Unmarshal(out, &actual); e != nil {
				t.Fatal(e, string(out))
			}
			if !actual.SIDChanged || string(actual.Reset.Get("clients")) != "[]" || string(actual.Reset.Get("tools")) != "[]" || string(actual.Reset.Get("commands")) != "[]" || string(actual.Reset.Get("key")) != "19" {
				t.Fatal("actual Source clear reset", string(out))
			}
			if !hold && !actual.SameClient {
				t.Fatal("clear replaced actual cached Source peer", string(out))
			}
			if !bytes.Contains(actual.After.Get("tools"), []byte(`"mcp__owned-peer__inspect"`)) || string(actual.After.Get("commands")) != `["mcp__owned-peer__review"]` || string(actual.After.Get("resources")) != `["owned-peer"]` {
				t.Fatal("actual Source rerender did not restore catalog", string(out))
			}
			if p.initialize.Load() != 1 || p.tools.Load() != 1 || p.prompts.Load() != 1 || p.resources.Load() != 1 {
				t.Fatal("clear reinitialized physical cached Source peer", p.initialize.Load(), p.tools.Load(), p.prompts.Load(), p.resources.Load(), string(out))
			}
		})
	}
}

func TestNativeTerminalClearMCPMemoizedPeerReturnsToNextActualModel(t *testing.T) {
	for _, hold := range []bool{false, true} {
		t.Run(map[bool]string{false: "connected", true: "pending-tools"}[hold], func(t *testing.T) {
			f := newNativePluginFixture(t)
			p := newClearMCPPhysicalPeer(t, hold)
			var models atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				models.Add(1)
				if !bytes.Contains(body, []byte(`"name":"mcp__owned-peer__inspect"`)) || !bytes.Contains(body, []byte("NEXT_CLEAR_MCP_HUMAN")) {
					t.Error("clear MCP actual next HTTP catalog missing", string(body))
				}
				emitPlanFixture(w, 1, "", "NEXT_CLEAR_MCP_COMPLETE")
			}))
			defer model.Close()
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "owned-clear-model"
			f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			changed := make(chan []sdkhost.ControlCommand, 32)
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Interactive: true, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnCommandsChanged: func(rows []sdkhost.ControlCommand) error { changed <- rows; return nil }, Arguments: PrintArguments{MCPConfigs: []string{string(rawMain(map[string]any{"mcpServers": map[string]any{"owned-peer": map[string]string{"type": "http", "url": p.server.URL}}}))}, ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual clear MCP model", HasCustomSystemPrompt: true, Tools: []string{""}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(m.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if e = m.StartSession(ctx); e != nil {
				t.Fatal(e)
			}
			wait := func(want bool) {
				t.Helper()
				for {
					select {
					case rows := <-changed:
						found := false
						for _, row := range rows {
							found = found || row.Name == "owned-peer:review (MCP)"
						}
						if found == want {
							return
						}
					case <-ctx.Done():
						t.Fatal("clear catalog publication", want, ctx.Err())
					}
				}
			}
			if hold {
				select {
				case <-p.held:
				case <-ctx.Done():
					t.Fatal("initial physical tools/list missing")
				}
			} else {
				wait(true)
			}
			for {
				select {
				case <-changed:
					continue
				default:
					goto drained
				}
			}
		drained:
			if _, e = m.TerminalClear(ctx); e != nil {
				t.Fatal(e)
			}
			wait(false)
			if e = m.clearNativeMCPForTerminalOnOwner(ctx, nil); e != cooperative.ErrRestricted {
				t.Fatal("nil clear bypass", e)
			}
			if hold {
				p.releaseOnce.Do(func() { close(p.release) })
			}
			wait(true)
			if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				peers, e := m.mcpTools.AgentConnections(scope)
				if e != nil {
					return e
				}
				if len(peers) != 1 || peers[0].Status != "connected" {
					t.Error("clear retained duplicate/public pending peer", peers)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "NEXT_CLEAR_MCP_HUMAN"})})
			if e = m.RunTurn(ctx, user); e != nil {
				t.Fatal(e)
			}
			if models.Load() != 1 || p.initialize.Load() != 1 || p.tools.Load() != 1 || p.prompts.Load() != 1 || p.resources.Load() != 1 {
				t.Fatal("clear replaced cached physical peer", models.Load(), p.initialize.Load(), p.tools.Load(), p.prompts.Load(), p.resources.Load())
			}
			foreign := cooperative.New()
			defer foreign.Close()
			if e = foreign.Run(ctx, func(scope *cooperative.Scope) error { return m.clearNativeMCPForTerminalOnOwner(ctx, scope) }); e != cooperative.ErrRestricted {
				t.Fatal("foreign clear bypass", e)
			}
			if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				m.mcpBorrowed = true
				defer func() { m.mcpBorrowed = false }()
				return m.clearNativeMCPForTerminalOnOwner(ctx, scope)
			}); e != cooperative.ErrRestricted {
				t.Fatal("borrowed clear changed parent MCP", e)
			}
		})
	}
}
