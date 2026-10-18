package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualPublicPrintMCPAuthenticationToolReturnsURLAndEOFJoinsListener(t *testing.T) {
	runPublicMCPAuthenticationTool(t, false)
}
func TestActualSessionMCPAuthenticationGlobalCloseCancelsHeldMetadataBeforeJoin(t *testing.T) {
	runPublicMCPAuthenticationTool(t, true)
}
func runPublicMCPAuthenticationTool(t *testing.T, heldMetadata bool) {
	t.Helper()
	var holdMetadata atomic.Bool
	metadataStarted, metadataCancelled := make(chan struct{}), make(chan struct{})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	var oauthTokens, models atomic.Int32
	var peer *httptest.Server
	peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+peer.URL+`/.well-known/oauth-protected-resource/mcp"`)
			w.WriteHeader(401)
		case "/.well-known/oauth-protected-resource/mcp":
			if holdMetadata.Load() {
				close(metadataStarted)
				<-r.Context().Done()
				close(metadataCancelled)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": peer.URL + "/mcp", "authorization_servers": []string{peer.URL}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": peer.URL, "authorization_endpoint": peer.URL + "/authorize", "token_endpoint": peer.URL + "/token", "registration_endpoint": peer.URL + "/register", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}})
		case "/register":
			var registration map[string]any
			_ = json.NewDecoder(r.Body).Decode(&registration)
			registration["client_id"] = "public-auth-tool-client"
			_ = json.NewEncoder(w).Encode(registration)
		case "/token":
			oauthTokens.Add(1)
			t.Error("headless EOF completed OAuth without user callback")
			w.WriteHeader(400)
		default:
			t.Error("unowned MCP authentication endpoint", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer peer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := models.Add(1)
		switch n {
		case 1:
			if !bytes.Contains(obj(body).Get("tools"), []byte(`"name":"mcp__auth__authenticate"`)) {
				t.Error("public print did not consume actual needs-auth tool catalog", string(body))
			}
			response(w, "actual-auth-start", "mcp__auth__authenticate", map[string]any{"ignored": "empty Source schema strips this"}, "")
		case 2:
			if heldMetadata {
				t.Error("Session global close sent model after cancelled authentication")
				response(w, "unexpected-late-auth", "", nil, "invalid late response")
				return
			}
			if !bytes.Contains(body, []byte("Ask the user to open this URL in their browser to authorize the auth MCP server:")) || !bytes.Contains(body, []byte("/authorize?")) {
				t.Error("public next model did not receive real authorization URL", string(body))
			}
			response(w, "actual-auth-link", "", nil, "PUBLIC_AUTH_LINK_RETURNED_BEFORE_BROWSER_319")
		default:
			t.Error("unexpected public auth model request", n)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	browser := filepath.Join(o.Environment["HOME"], "private-browser")
	if err := os.WriteFile(browser, []byte("#!/bin/sh\nprintf 'opened' > \"$AUTH_BROWSER_RECEIPT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	browserReceipt := filepath.Join(o.Environment["HOME"], "browser-receipt")
	for _, environment := range []map[string]string{o.Environment, o.Native.Environment} {
		environment["BROWSER"] = browser
		environment["AUTH_BROWSER_RECEIPT"] = browserReceipt
	}
	configuration := string(raw(map[string]any{"mcpServers": map[string]any{"auth": map[string]any{"type": "http", "url": peer.URL + "/mcp", "oauth": map[string]any{"callbackPort": port}}}}))
	arguments := []string{"--print", "--model", "fixture-model", "--thinking", "disabled", "--system-prompt", "Return the actual MCP authentication URL", "--tools", "mcp__auth__authenticate", "--permission-mode", "default", "--strict-mcp-config", "--mcp-config", configuration, "--", "Start MCP authentication"}
	command, _, err := ParseCommand(arguments, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if heldMetadata {
		o.Arguments = command.Arguments
		session, err := NewSession(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close(context.Background())
		holdMetadata.Store(true)
		submitted := make(chan error, 1)
		go func() { submitted <- session.Submit(ctx, tui.Input{Text: "Start MCP authentication"}, nil) }()
		select {
		case <-metadataStarted:
		case err := <-submitted:
			t.Fatal("real auth metadata was not held", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		closed := make(chan error, 1)
		go func() { closed <- session.Close(ctx) }()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("Session global close did not join actual auth worker", ctx.Err())
		}
		select {
		case <-metadataCancelled:
		case <-ctx.Done():
			t.Fatal("actual metadata HTTP survived Session close")
		}
		select {
		case <-submitted:
		case <-ctx.Done():
			t.Fatal("actual ASK survived Session close")
		}
		if models.Load() != 1 || oauthTokens.Load() != 0 {
			t.Fatal("Session close performed late model/token HTTP", models.Load(), oauthTokens.Load())
		}
		return
	}
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || !strings.Contains(output.String(), "PUBLIC_AUTH_LINK_RETURNED_BEFORE_BROWSER_319") {
		t.Fatal("public print auth failed or held EOF", code, err, output.String())
	}
	if _, err := os.Stat(browserReceipt); !os.IsNotExist(err) || models.Load() != 2 || oauthTokens.Load() != 0 {
		t.Fatal("print auth opened browser or performed unexpected requests", err, models.Load(), oauthTokens.Load())
	}
	connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("headless EOF retained real OAuth callback listener")
	}
}
