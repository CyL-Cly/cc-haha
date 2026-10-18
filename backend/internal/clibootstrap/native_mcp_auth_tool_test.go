package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func authenticationResultMessage(body []byte) string {
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &request) != nil {
		return ""
	}
	for i := len(request.Messages) - 1; i >= 0; i-- {
		var blocks []struct{ Type, Content string }
		if json.Unmarshal(request.Messages[i].Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == "tool_result" && strings.Contains(block.Content, "authorize the auth MCP server") {
				return block.Content
			}
		}
	}
	return ""
}

func waitAuthenticationPeer(t *testing.T, ctx context.Context, x *terminalMCPAuthFixture, want string) {
	t.Helper()
	for {
		found := false
		if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			rows, err := x.m.mcpTools.Status(scope)
			for _, row := range rows {
				found = found || row.String("name") == "auth" && row.String("status") == want
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if found {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual peer did not reach", want, ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}

func TestNativeMCPAuthToolReturnsURLBeforeBrowserAndPublishesToNextHarness(t *testing.T) {
	for _, mode := range []string{"normal", "cancel-turn", "headless"} {
		t.Run(mode, func(t *testing.T) {
			cancelTurn := mode == "cancel-turn"
			addresses := make(chan string, 1)
			heldModel := make(chan struct{})
			x := newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{headless: mode == "headless", tools: []string{"mcp__auth__authenticate", "mcp__auth__inspect"}, model: func(x *terminalMCPAuthFixture, w http.ResponseWriter, r *http.Request, body []byte) {
				n := x.models.Add(1)
				var request protocol.JSONObject
				_ = request.UnmarshalJSON(body)
				catalog := request.Get("tools")
				switch n {
				case 1:
					if !bytes.Contains(catalog, []byte(`"name":"mcp__auth__authenticate"`)) || bytes.Contains(catalog, []byte(`"name":"mcp__auth__inspect"`)) || !bytes.Contains(catalog, []byte("requires authentication")) {
						t.Error("actual needs-auth catalog lacks Source authentication tool", string(catalog))
					}
					emitPlanFixture(w, int(n), "mcp__auth__authenticate", "")
				case 2:
					message := authenticationResultMessage(body)
					parts := strings.Split(message, "\n\n")
					if len(parts) != 3 || parts[0] != "Ask the user to open this URL in their browser to authorize the auth MCP server:" || parts[2] != "Once they complete the flow, the server's tools will become available automatically." {
						t.Error("actual tool result differs from Source", message)
						emitPlanFixture(w, int(n), "", "Invalid result")
						return
					}
					addresses <- parts[1]
					if cancelTurn {
						close(heldModel)
						<-r.Context().Done()
						return
					}
					emitPlanFixture(w, int(n), "", "URL returned without waiting for a browser")
				case 3:
					if !bytes.Contains(catalog, []byte(`"name":"mcp__auth__inspect"`)) || bytes.Contains(catalog, []byte(`"name":"mcp__auth__authenticate"`)) {
						t.Error("background OAuth did not replace actual next Harness catalog", string(catalog))
					}
					emitPlanFixture(w, int(n), "mcp__auth__inspect", "")
				case 4:
					if !bytes.Contains(body, []byte("authenticated physical MCP result")) {
						t.Error("real authenticated MCP result missing from next model")
					}
					emitPlanFixture(w, int(n), "", "Authenticated tool completed")
				default:
					t.Error("unexpected actual model call", n)
				}
			}})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			turn, abort := context.WithCancel(ctx)
			defer abort()
			done := make(chan error, 1)
			go func() {
				done <- x.m.RunTurn(turn, x.user("Start authentication using the actual MCP authentication tool"))
			}()
			var address string
			select {
			case address = <-addresses:
			case err := <-done:
				t.Fatal("actual auth tool failed before returning URL", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if cancelTurn {
				select {
				case <-heldModel:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				abort()
			}
			select {
			case err := <-done:
				if !cancelTurn && err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("query waited for five-minute authorization", ctx.Err())
			}
			if _, err := os.Stat(x.browser); !os.IsNotExist(err) || x.tokens.Load() != 0 || x.connects.Load() != 0 {
				t.Fatal("tool unexpectedly opened browser or completed OAuth", err, x.tokens.Load(), x.connects.Load())
			}
			response, err := http.Get(terminalMCPAuthCallback(t, address))
			if err != nil {
				t.Fatal("query cancellation incorrectly cancelled independent OAuth listener", err)
			}
			_ = response.Body.Close()
			waitAuthenticationPeer(t, ctx, x, "connected")
			if err := x.m.RunTurn(ctx, x.user("Use the now authenticated inspect tool")); err != nil {
				t.Fatal(err)
			}
			if x.tokens.Load() != 1 || x.connects.Load() != 1 || x.calls.Load() != 1 || x.models.Load() != 4 {
				t.Fatal("actual background OAuth/next Harness counts", x.tokens.Load(), x.connects.Load(), x.calls.Load(), x.models.Load())
			}
		})
	}
}

func TestNativeMCPAuthToolGlobalCloseCancelsHeldMetadataBeforeURL(t *testing.T) {
	x := newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{holdMetadata: true, tools: []string{"mcp__auth__authenticate"}, model: func(x *terminalMCPAuthFixture, w http.ResponseWriter, r *http.Request, body []byte) {
		n := x.models.Add(1)
		if n != 1 {
			t.Error("shutdown allowed additional model request", n)
		}
		emitPlanFixture(w, int(n), "mcp__auth__authenticate", "")
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	turn, abort := context.WithCancel(ctx)
	defer abort()
	done := make(chan error, 1)
	go func() { done <- x.m.RunTurn(turn, x.user("Start authentication")) }()
	select {
	case <-x.metadataStarted:
	case err := <-done:
		t.Fatal("no actual held metadata request", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Match Session.closeWithReason: cancel the actual model/ASK lifetime first,
	// then abort and join the independent authorization jobs before Host.Join.
	abort()
	closed := make(chan struct{})
	go func() { x.m.BeginNativeTerminalMCPAuthClose(); close(closed) }()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("global close failed to join held auth metadata", ctx.Err())
	}
	select {
	case <-x.metadataCancelled:
	case <-ctx.Done():
		t.Fatal("actual metadata HTTP request survived close")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("actual ASK survived global auth close")
	}
	connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", x.port), 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("shutdown created callback listener after metadata cancellation")
	}
	if x.tokens.Load() != 0 || x.models.Load() != 1 || x.connects.Load() != 0 {
		t.Fatal("shutdown performed late provider work", x.tokens.Load(), x.models.Load(), x.connects.Load())
	}
}

func TestNativeMCPAuthToolRejectsNilForeignRetainedCancelledAndClosedCallContexts(t *testing.T) {
	x := newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{tools: []string{"mcp__auth__authenticate"}})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var tool queryloop.RegisteredTool
	var retained *cooperative.Scope
	if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		retained = scope
		_, tools, err := x.m.mcpTools.Catalog(scope)
		if err != nil {
			return err
		}
		for _, candidate := range tools {
			if candidate.Name == "mcp__auth__authenticate" {
				tool = candidate
			}
		}
		if tool.StartCallJSON == nil {
			return fmt.Errorf("auth tool missing from actual catalog")
		}
		for _, opaque := range []any{nil, (*builtinwrite.CallContext)(nil), &builtinwrite.CallContext{}} {
			if _, err := tool.StartCallJSON(ctx, protocol.NewJSONObject(), queryloop.JSONToolCallContext{Opaque: opaque}); !errors.Is(err, cooperative.ErrRestricted) {
				return fmt.Errorf("unowned auth context admitted: %w", err)
			}
		}
		call := queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}}
		if _, err := tool.StartCallJSON(nil, protocol.NewJSONObject(), call); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("nil auth lifetime admitted: %w", err)
		}
		cancelled, abort := context.WithCancel(ctx)
		abort()
		if _, err := tool.StartCallJSON(cancelled, protocol.NewJSONObject(), call); !errors.Is(err, context.Canceled) {
			return fmt.Errorf("cancelled auth query admitted: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.StartCallJSON(ctx, protocol.NewJSONObject(), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: retained}}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained owner scope admitted", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := tool.StartCallJSON(ctx, protocol.NewJSONObject(), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign auth owner admitted: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	x.m.BeginNativeTerminalMCPAuthClose()
	if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := tool.StartCallJSON(ctx, protocol.NewJSONObject(), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
		if !errors.Is(err, cooperative.ErrClosed) {
			return fmt.Errorf("closed auth job lifetime admitted: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if x.tokens.Load() != 0 || x.connects.Load() != 0 || x.models.Load() != 0 {
		t.Fatal("rejected auth capabilities performed provider work")
	}
}
