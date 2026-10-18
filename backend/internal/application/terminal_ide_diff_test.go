package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/coder/websocket"
)

type terminalDiffPhysicalPeer struct {
	server              *httptest.Server
	mu                  sync.Mutex
	calls               []protocol.JSONObject
	open, closed        chan struct{}
	openOnce, closeOnce sync.Once
	profile, path       string
	receiptPath         string
}

func newTerminalDiffPhysicalPeer(t *testing.T, profile string) *terminalDiffPhysicalPeer {
	t.Helper()
	p := &terminalDiffPhysicalPeer{profile: profile, open: make(chan struct{}), closed: make(chan struct{})}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"mcp"}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		defer p.closeOnce.Do(func() { close(p.closed) })
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			input := obj(data)
			p.mu.Lock()
			p.calls = append(p.calls, input.Clone())
			if p.receiptPath != "" {
				counts := map[string]int{"open": 0, "close": 0, "cancel": 0}
				for _, row := range p.calls {
					if row.String("method") == "notifications/cancelled" {
						counts["cancel"]++
					}
					name := obj(row.Get("params")).String("name")
					if name == "openDiff" {
						counts["open"]++
					}
					if name == "close_tab" {
						counts["close"]++
					}
				}
				_ = os.WriteFile(p.receiptPath, raw(counts), 0600)
			}
			p.mu.Unlock()
			if input.Get("id") == nil {
				continue
			}
			result := json.RawMessage(`{}`)
			switch input.String("method") {
			case "initialize":
				result = []byte(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"Actual Diff IDE","version":"1"}}`)
			case "tools/list":
				result = []byte(`{"tools":[]}`)
			case "tools/call":
				params := obj(input.Get("params"))
				args := obj(params.Get("arguments"))
				switch params.String("name") {
				case "openDiff":
					if args.String("old_file_path") != p.path || args.String("new_file_path") != p.path || !strings.HasPrefix(args.String("tab_name"), "✻ [Claude Code] ") {
						t.Error("actual direct IDE RPC paths/tab missing", string(data))
					}
					p.openOnce.Do(func() { close(p.open) })
					switch profile {
					case "disconnect":
						return
					case "public":
						if p.count("openDiff") == 1 {
							result = raw(map[string]any{"content": []any{map[string]any{"type": "text", "text": "FILE_SAVED"}, map[string]any{"type": "text", "text": "head\nUSER_IDE_PUBLIC_SAVE_526\nlast\n"}}})
						} else {
							continue
						}
					case "saved", "write-saved", "positive-held-close":
						result = raw(map[string]any{"content": []any{map[string]any{"type": "text", "text": "FILE_SAVED"}, map[string]any{"type": "text", "text": "head\nUSER_IDE_EDITED_526\nlast\n"}}})
					case "saved-on-disk", "write-saved-on-disk":
						if err := os.WriteFile(p.path, []byte("head\nUSER_IDE_PHYSICAL_SAVE_526\nlast\n"), 0600); err != nil {
							t.Error(err)
						}
						result = raw(map[string]any{"content": []any{map[string]any{"type": "text", "text": "FILE_SAVED"}, map[string]any{"type": "text", "text": "head\nUSER_IDE_PHYSICAL_SAVE_526\nlast\n"}}})
					case "rejected":
						result = []byte(`{"content":[{"type":"text","text":"DIFF_REJECTED"}]}`)
					case "malformed":
						result = []byte(`{"content":[{"type":"text","text":"UNKNOWN"}]}`)
					case "held":
						continue
					default:
						result = []byte(`{"content":[{"type":"text","text":"TAB_CLOSED"}]}`)
					}
				case "close_tab":
					result = []byte(`{"content":[]}`)
					if profile == "held" || profile == "positive-held-close" || profile == "public" && p.count("openDiff") > 1 {
						continue
					}
				default:
					t.Error("unexpected IDE tool", params.String("name"))
				}
			default:
				t.Error("unexpected direct IDE request", input.String("method"))
			}
			packet := protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: raw("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result})
			if err = conn.Write(r.Context(), websocket.MessageText, raw(packet)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}
func (p *terminalDiffPhysicalPeer) count(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, call := range p.calls {
		if call.String("method") == "tools/call" && obj(call.Get("params")).String("name") == name {
			count++
		}
	}
	return count
}

func terminalDiffNativeSessionOptions(t *testing.T, model string, peer *terminalDiffPhysicalPeer) Options {
	t.Helper()
	o := nativeSessionOptions(t, model)
	o.Interactive = true
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = []string{"Read,Edit,Write"}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"] = "1"
		delete(env, "TERM_PROGRAM")
	}
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"alwaysThinkingEnabled":false,"permissions":{"allow":["Read"]},"futureDiffUser":{"keep":true}}`), 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "autoConnectIde": true, "diffTool": "auto", "futureDiffGlobal": map[string]any{"keep": true}, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	port, _ := strconv.Atoi(strings.TrimPrefix(peer.server.URL, "http://127.0.0.1:"))
	lock := filepath.Join(config, "ide", strconv.Itoa(port)+".lock")
	_ = os.MkdirAll(filepath.Dir(lock), 0700)
	_ = os.WriteFile(lock, raw(map[string]any{"workspaceFolders": []string{o.WorkDir}, "pid": os.Getpid(), "ideName": "Actual Diff IDE", "transport": "ws", "authToken": "never-real-diff-token"}), 0600)
	peer.path = filepath.Join(o.WorkDir, "diff-permission.txt")
	_ = os.WriteFile(peer.path, []byte("head\nold\nlast\n"), 0600)
	return o
}
func waitTerminalDiffActualConnection(t *testing.T, ctx context.Context, s *Session) {
	t.Helper()
	for {
		state, err := s.process.TerminalConfigState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if string(state.Get("isConnectedToIde")) == "true" {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual IDE not published", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestTerminalConfigDiffActualPermissionMountSourceResponseFileConsumerAndNextHuman(t *testing.T) {
	for _, profile := range []string{"closed", "saved", "write-saved", "saved-on-disk", "write-saved-on-disk", "rejected", "malformed", "terminal", "positive-held-close", "disconnect"} {
		t.Run(profile, func(t *testing.T) {
			peer := newTerminalDiffPhysicalPeer(t, profile)
			var requests atomic.Int32
			var errorResult atomic.Bool
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				index := requests.Add(1)
				switch index {
				case 1:
					response(w, "ide-diff-read", "Read", map[string]any{"file_path": peer.path}, "")
				case 2:
					if strings.HasPrefix(profile, "write-") {
						response(w, "ide-diff-write", "Write", map[string]any{"file_path": peer.path, "content": "head\nnew\nlast\n"}, "")
					} else {
						response(w, "ide-diff-edit", "Edit", map[string]any{"file_path": peer.path, "old_string": "old", "new_string": "new"}, "")
					}
				case 3:
					for _, message := range array(obj(body).Get("messages")) {
						for _, block := range array(message.Get("content")) {
							if block.String("type") == "tool_result" && string(block.Get("is_error")) == "true" {
								errorResult.Store(true)
							}
						}
					}
					response(w, "ide-diff-finish", "", nil, "IDE_DIFF_PHYSICAL_DONE_526")
				default:
					response(w, "ide-diff-next", "", nil, "IDE_DIFF_SAME_HARNESS_NEXT_526")
				}
			}))
			defer provider.Close()
			o := terminalDiffNativeSessionOptions(t, provider.URL, peer)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			waitTerminalDiffActualConnection(t, ctx, s)
			var panel *tui.LocalPanel
			var mounted atomic.Int32
			emit := func(event tui.Event) error {
				if event.LocalPanel != nil {
					copy := *event.LocalPanel
					panel = &copy
				}
				if event.Kind == tui.PermissionUpdated && event.Permission != nil && event.Permission.IDEName != "" {
					mounted.Add(1)
				}
				manual := profile == "terminal" && event.Kind == tui.PermissionAsked || (profile == "malformed" || profile == "disconnect") && event.Kind == tui.PermissionUpdated && event.Permission != nil && event.Permission.IDEName == ""
				if manual {
					id := event.Permission.ID
					go func() {
						if err := s.RespondPermission(ctx, id, tui.PermissionDecision{Allowed: true}); err != nil {
							t.Error(err)
						}
					}()
				}
				return nil
			}
			if _, err = s.InvokeTerminalCommand(ctx, "config", "", emit); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range panel.Rows {
				found = found || row.ID == "diffTool"
			}
			if !found {
				t.Fatal("actual connected IDE did not expose Config Diff row")
			}
			if profile == "terminal" {
				if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "next-config", Row: "diffTool"}, emit); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "commit-config"}, emit); err != nil {
				t.Fatal(err)
			}
			if err = s.Submit(ctx, tui.Input{Text: "Read then perform the actual IDE diff", Mode: "prompt"}, emit); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(peer.path)
			if err != nil {
				t.Fatal(err)
			}
			want := "head\nnew\nlast\n"
			wantError := false
			switch profile {
			case "saved", "positive-held-close":
				want = "head\nUSER_IDE_EDITED_526\nlast\n"
			case "write-saved":
				want = "head\nUSER_IDE_EDITED_526\nlast"
			case "saved-on-disk", "write-saved-on-disk":
				want = "head\nUSER_IDE_PHYSICAL_SAVE_526\nlast\n"
				wantError = true
			case "rejected":
				want = "head\nold\nlast\n"
				wantError = true
			}
			if string(data) != want || errorResult.Load() != wantError {
				t.Fatal("actual file/tool error differs from original consumer", string(data), want, errorResult.Load(), wantError)
			}
			if err = s.Submit(ctx, tui.Input{Text: "Next human on the same Harness", Mode: "prompt"}, emit); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 4 {
				t.Fatal("local UI crossed model boundary", requests.Load())
			}
			if profile == "terminal" {
				if peer.count("openDiff") != 0 || mounted.Load() != 0 {
					t.Fatal("terminal setting still mounted IDE diff")
				}
			} else {
				if peer.count("openDiff") != 1 || mounted.Load() != 1 {
					t.Fatal("actual permission mount missing", peer.count("openDiff"), mounted.Load())
				}
				closes := 1
				if profile == "rejected" {
					closes = 2
				}
				if profile == "disconnect" {
					closes = 0
				}
				if peer.count("close_tab") != closes {
					t.Fatal("Source cleanup RPC count differs", peer.count("close_tab"), closes)
				}
			}
			if err = s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-peer.closed:
			case <-ctx.Done():
				t.Fatal("actual IDE socket not joined")
			}
		})
	}
}

func TestTerminalIDEDiffGlobalCloseSendsCleanupCancelsBothHeldRPCsAndJoins(t *testing.T) {
	peer := newTerminalDiffPhysicalPeer(t, "held")
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			response(w, "held-ide-read", "Read", map[string]any{"file_path": peer.path}, "")
		} else {
			response(w, "held-ide-edit", "Edit", map[string]any{"file_path": peer.path, "old_string": "old", "new_string": "new"}, "")
		}
	}))
	defer provider.Close()
	o := terminalDiffNativeSessionOptions(t, provider.URL, peer)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	waitTerminalDiffActualConnection(t, ctx, s)
	done := make(chan error, 1)
	go func() {
		done <- s.Submit(ctx, tui.Input{Text: "Read then hold actual IDE permission", Mode: "prompt"}, func(tui.Event) error { return nil })
	}()
	select {
	case <-peer.open:
	case <-ctx.Done():
		t.Fatal("actual held openDiff not reached")
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("actual permission/ASK not joined")
	}
	select {
	case <-peer.closed:
	case <-ctx.Done():
		t.Fatal("actual IDE RPC/transport not joined")
	}
	if peer.count("close_tab") != 1 || requests.Load() != 2 {
		t.Fatal("held cleanup not physically sent or extra model after close", peer.count("close_tab"), requests.Load())
	}
	data, _ := os.ReadFile(peer.path)
	if string(data) != "head\nold\nlast\n" {
		t.Fatal("closed permission late result changed file", string(data))
	}
}
