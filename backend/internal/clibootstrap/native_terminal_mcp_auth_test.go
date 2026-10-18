package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

type terminalMCPAuthFixture struct {
	f                                        nativePluginFixture
	m                                        *MainPrint
	peer                                     *httptest.Server
	port                                     int
	browser                                  string
	tokens, revokes, connects, calls, models atomic.Int32
	mcpAttempts                              atomic.Int32
	tokenStarted, tokenCancelled             chan struct{}
	holdToken                                bool
	holdMetadata                             atomic.Bool
	metadataStarted, metadataCancelled       chan struct{}
}

type terminalMCPAuthFixtureOptions struct {
	holdToken          bool
	holdMetadata       bool
	headless           bool
	allowAnonymous     bool
	prepare            func(*terminalMCPAuthFixture)
	tools              []string
	model              func(*terminalMCPAuthFixture, http.ResponseWriter, *http.Request, []byte)
	onCallbackListener func(net.Listener)
}

func newTerminalMCPAuthFixture(t *testing.T, holdToken bool) *terminalMCPAuthFixture {
	t.Helper()
	return newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{holdToken: holdToken})
}

func newTerminalMCPAuthFixtureWithOptions(t *testing.T, options terminalMCPAuthFixtureOptions) *terminalMCPAuthFixture {
	t.Helper()
	x := &terminalMCPAuthFixture{f: newNativePluginFixture(t), holdToken: options.holdToken, tokenStarted: make(chan struct{}), tokenCancelled: make(chan struct{}), metadataStarted: make(chan struct{}), metadataCancelled: make(chan struct{})}
	f := x.f
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.write(t, filepath.Join(f.config, ".credentials.json"), `{"future":{"keep":"private-auth-fixture"}}`)
	seedNativeReleaseWarmFixture(t, f, "fixture")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	x.port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	x.browser = filepath.Join(f.home, "browser-url")
	browser := filepath.Join(f.home, "private-browser")
	f.write(t, browser, "#!/bin/sh\nprintf '%s' \"$1\" > \"$AUTH_BROWSER_RECEIPT\"\n")
	if err := os.Chmod(browser, 0700); err != nil {
		t.Fatal(err)
	}
	f.n.Environment["BROWSER"], f.n.Environment["AUTH_BROWSER_RECEIPT"] = browser, x.browser
	x.peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			if x.holdMetadata.Load() {
				select {
				case <-x.metadataStarted:
				default:
					close(x.metadataStarted)
				}
				<-r.Context().Done()
				select {
				case <-x.metadataCancelled:
				default:
					close(x.metadataCancelled)
				}
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": x.peer.URL + "/mcp", "authorization_servers": []string{x.peer.URL}, "scopes_supported": []string{"read", "offline_access"}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": x.peer.URL, "authorization_endpoint": x.peer.URL + "/authorize", "token_endpoint": x.peer.URL + "/token", "registration_endpoint": x.peer.URL + "/register", "revocation_endpoint": x.peer.URL + "/revoke", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"read", "offline_access"}})
		case "/register":
			var registration map[string]any
			_ = json.NewDecoder(r.Body).Decode(&registration)
			registration["client_id"] = "private-native-auth-client"
			_ = json.NewEncoder(w).Encode(registration)
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "private-auth-code" || r.Form.Get("code_verifier") == "" || r.Form.Get("redirect_uri") != fmt.Sprintf("http://localhost:%d/callback", x.port) {
				t.Error("actual PKCE exchange lost original callback", r.Form)
			}
			x.tokens.Add(1)
			if x.holdToken {
				close(x.tokenStarted)
				<-r.Context().Done()
				close(x.tokenCancelled)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "private-native-auth-access", "refresh_token": "private-native-auth-refresh", "token_type": "Bearer", "expires_in": 3600, "scope": "read offline_access"})
		case "/revoke":
			_ = r.ParseForm()
			kind := r.Form.Get("token_type_hint")
			if kind != "refresh_token" && kind != "access_token" {
				t.Error("real revocation lost token hint", r.Form)
			}
			x.revokes.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/mcp":
			x.mcpAttempts.Add(1)
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if !options.allowAnonymous && r.Header.Get("Authorization") != "Bearer private-native-auth-access" {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+x.peer.URL+`/.well-known/oauth-protected-resource/mcp"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var message protocol.JSONObject
			body, _ := io.ReadAll(r.Body)
			_ = message.UnmarshalJSON(body)
			if message.Get("id") == nil {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			var result json.RawMessage
			switch message.String("method") {
			case "initialize":
				x.connects.Add(1)
				result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-auth","version":"1"}}`)
			case "tools/list":
				result = []byte(`{"tools":[{"name":"inspect","inputSchema":{"type":"object","properties":{}}}]}`)
			case "tools/call":
				x.calls.Add(1)
				result = []byte(`{"content":[{"type":"text","text":"authenticated physical MCP result"}]}`)
			default:
				t.Error("unexpected authenticated MCP method", message)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(x.peer.Close)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if options.model != nil {
			options.model(x, w, r, body)
			return
		}
		var request protocol.JSONObject
		if err := request.UnmarshalJSON(body); err != nil {
			t.Error("invalid actual Harness body", err)
		}
		catalog := request.Get("tools")
		n := x.models.Add(1)
		if n <= 2 && !bytes.Contains(catalog, []byte(`"name":"mcp__auth__inspect"`)) {
			t.Error("authenticated catalog missing from actual Harness POST")
		}
		switch n {
		case 1:
			emitPlanFixture(w, int(n), "mcp__auth__inspect", "")
		case 2:
			if !bytes.Contains(body, []byte("authenticated physical MCP result")) {
				t.Error("real tool result missing from next model POST")
			}
			emitPlanFixture(w, int(n), "", "Authenticated MCP executed")
		case 3:
			if bytes.Contains(catalog, []byte(`"name":"mcp__auth__inspect"`)) {
				t.Error("cleared authentication retained callable catalog")
			}
			emitPlanFixture(w, int(n), "", "Cleared MCP excluded")
		default:
			t.Error("unexpected model request", n)
		}
	}))
	t.Cleanup(model.Close)
	f.n.Environment["ANTHROPIC_BASE_URL"], f.n.Environment["ANTHROPIC_API_KEY"] = model.URL, "private-auth-model-fixture"
	config := rawMain(map[string]any{"mcpServers": map[string]any{"auth": map[string]any{"type": "http", "url": x.peer.URL + "/mcp", "oauth": map[string]any{"callbackPort": x.port}}}})
	if options.prepare != nil {
		options.prepare(x)
	}
	disabled := false
	selectedTools := options.tools
	if selectedTools == nil {
		selectedTools = []string{"mcp__auth__inspect"}
	}
	x.m, err = NewMainPrint(MainPrintOptions{Interactive: !options.headless, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{MCPConfigs: []string{string(config)}, Model: "fixture-model", ComputerUse: &disabled, SystemPrompt: "Actual terminal OAuth consumer", Tools: selectedTools, PermissionMode: "bypassPermissions", MaxTurns: 4, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	x.m.nativeTerminalMCPAuthLife.onCallbackListener = options.onCallbackListener
	t.Cleanup(x.m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := x.m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		ready := false
		if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			rows, err := x.m.mcpTools.Status(scope)
			for _, row := range rows {
				ready = ready || row.String("name") == "auth" && row.String("status") != "pending"
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual auth-required discovery never settled", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	x.holdMetadata.Store(options.holdMetadata)
	return x
}

func (x *terminalMCPAuthFixture) user(text string) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(x.m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(text)}))})
}

func startTerminalMCPAuth(t *testing.T, ctx context.Context, x *terminalMCPAuthFixture) (string, func(string), <-chan error) {
	t.Helper()
	addresses, waiters, done := make(chan string, 1), make(chan func(string), 1), make(chan error, 1)
	go func() {
		done <- x.m.StartTerminalMCPAuthentication(ctx, "auth", func(address string) { addresses <- address }, func(submit func(string)) { waiters <- submit })
	}()
	var address string
	select {
	case address = <-addresses:
	case err := <-done:
		t.Fatal("actual OAuth failed before URL", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var submit func(string)
	select {
	case submit = <-waiters:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return address, submit, done
}

func terminalMCPAuthCallback(t *testing.T, address string) string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil || parsed.Query().Get("code_challenge_method") != "S256" || parsed.Query().Get("state") == "" {
		t.Fatal("invalid actual OAuth authorization URL", address, err)
	}
	return parsed.Query().Get("redirect_uri") + "?code=private-auth-code&state=" + url.QueryEscape(parsed.Query().Get("state"))
}

// Probe only the captured listener, never its reusable numeric OS port. An
// empty Control callback cannot affect Serve or its deadlines. Accept is safe
// only after Control has proved that this same listener is already closed.
func terminalMCPAuthCallbackListenerClosed(listener *net.TCPListener) error {
	if listener == nil {
		return errors.New("missing actual callback listener")
	}
	raw, err := listener.SyscallConn()
	if err != nil {
		return fmt.Errorf("actual callback listener SyscallConn: %w", err)
	}
	called := false
	err = raw.Control(func(uintptr) { called = true })
	if called || !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("actual callback listener raw-control: callback=%t error=%v", called, err)
	}
	connection, err := listener.Accept()
	if connection != nil {
		_ = connection.Close()
	}
	if connection != nil || !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("actual callback listener accept: connection=%t error=%v", connection != nil, err)
	}
	return nil
}

func terminalMCPAuthObservedListener(t *testing.T, ctx context.Context, observed <-chan net.Listener, port int) *net.TCPListener {
	t.Helper()
	select {
	case observedListener := <-observed:
		listener, ok := observedListener.(*net.TCPListener)
		if !ok || listener == nil || listener.Addr().String() != fmt.Sprintf("127.0.0.1:%d", port) {
			t.Fatal("observer did not retain the actual configured TCP callback listener", observedListener)
		}
		return listener
	case <-ctx.Done():
		t.Fatal("actual callback listener was not observed", ctx.Err())
		return nil
	}
}

func TestTerminalMCPAuthenticationListenerIdentityProofRejectsOpenAndAcceptsClosedResources(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	actual := listener.(*net.TCPListener)
	address := actual.Addr().String()
	if err := terminalMCPAuthCallbackListenerClosed(actual); err == nil {
		t.Fatal("listener identity proof accepted an independently open listener")
	}
	// The negative proof must leave the open listener untouched. In particular,
	// it must not call Accept, Close or SetDeadline to manufacture its answer.
	raw, err := actual.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := raw.Control(func(uintptr) { called = true }); err != nil || !called {
		t.Fatal("open-listener negative proof disturbed its resource", called, err)
	}
	if err := actual.Close(); err != nil {
		t.Fatal(err)
	}
	if err := terminalMCPAuthCallbackListenerClosed(actual); err != nil {
		t.Fatal("listener identity proof rejected a genuinely closed listener", err)
	}
	replacement, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal("closed independent listener did not release its OS port", err)
	}
	defer replacement.Close()
	if err := terminalMCPAuthCallbackListenerClosed(actual); err != nil {
		t.Fatal("replacement listener changed the old listener's closed identity", err)
	}
	if err := terminalMCPAuthCallbackListenerClosed(replacement.(*net.TCPListener)); err == nil {
		t.Fatal("listener identity proof borrowed closure from a different same-port listener")
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := terminalMCPAuthCallbackListenerClosed(replacement.(*net.TCPListener)); err != nil {
		t.Fatal("listener identity proof rejected the closed replacement", err)
	}
}

func TestTerminalMCPAuthenticationManualCallbackReconnectClearAndNextHarness(t *testing.T) {
	x := newTerminalMCPAuthFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if yes, err := x.m.TerminalMCPHasAuthentication(ctx, "auth"); err != nil || yes {
		t.Fatal("unsaved OAuth presented authenticated", yes, err)
	}
	address, submit, done := startTerminalMCPAuth(t, ctx, x)
	// Wait for the actual browser primitive before simulating human input. A
	// synchronously supplied callback can finish before its process starts.
	for {
		value, err := os.ReadFile(x.browser)
		if err == nil && string(value) == address {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("real private browser was not opened", string(value), err)
		case <-time.After(time.Millisecond):
		}
	}
	// Source ignores incomplete manual input and keeps the same live callback.
	submit("not a URL")
	submit("http://localhost/callback?state=missing-code")
	submit(terminalMCPAuthCallback(t, address))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("manual auth/reconnect did not join", ctx.Err())
	}
	if yes, err := x.m.TerminalMCPHasAuthentication(ctx, "auth"); err != nil || !yes {
		t.Fatal("actual secure-store token not visible", yes, err)
	}
	if x.tokens.Load() != 1 || x.connects.Load() != 1 {
		t.Fatal("actual OAuth/reconnect counts", x.tokens.Load(), x.connects.Load())
	}
	if err := x.m.RunTurn(ctx, x.user("Execute the authenticated inspect tool")); err != nil {
		t.Fatal(err)
	}
	if x.calls.Load() != 1 || x.models.Load() != 2 {
		t.Fatal("auth did not feed actual next Harness", x.calls.Load(), x.models.Load())
	}
	if err := x.m.ClearTerminalMCPAuthentication(ctx, "auth"); err != nil {
		t.Fatal(err)
	}
	if yes, err := x.m.TerminalMCPHasAuthentication(ctx, "auth"); err != nil || yes {
		t.Fatal("clear retained saved authentication", yes, err)
	}
	if x.revokes.Load() != 2 || x.connects.Load() != 1 {
		t.Fatal("clear skipped real revocation or reconnected", x.revokes.Load(), x.connects.Load())
	}
	if err := x.m.RunTurn(ctx, x.user("Continue after authentication was cleared")); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(x.f.config, ".credentials.json"))
	if err != nil || !bytes.Contains(stored, []byte("private-auth-fixture")) || bytes.Contains(stored, []byte("private-native-auth-access")) {
		t.Fatal("credential clear damaged unrelated fields or retained token", string(stored), err)
	}
	if x.models.Load() != 3 {
		t.Fatal("clear next Harness did not complete", x.models.Load())
	}
}

func TestTerminalMCPAuthenticationCancellationClosesListenerAndRejectsLateCallback(t *testing.T) {
	for _, kind := range []string{"dialog-cancel", "global-close", "held-token", "state-mismatch"} {
		t.Run(kind, func(t *testing.T) {
			observed := make(chan net.Listener, 1)
			x := newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{holdToken: kind == "held-token", onCallbackListener: func(listener net.Listener) { observed <- listener }})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			flow, abort := context.WithCancel(ctx)
			defer abort()
			address, submit, done := startTerminalMCPAuth(t, flow, x)
			actualListener := terminalMCPAuthObservedListener(t, ctx, observed, x.port)
			rawListener, err := actualListener.SyscallConn()
			if err != nil {
				t.Fatal("observed live callback listener SyscallConn failed", err)
			}
			openCallbackRan := false
			if err := rawListener.Control(func(uintptr) { openCallbackRan = true }); err != nil || !openCallbackRan {
				t.Fatal("observed actual callback listener was not open before cancellation", openCallbackRan, err)
			}
			callback := terminalMCPAuthCallback(t, address)
			switch kind {
			case "dialog-cancel":
				abort()
			case "global-close":
				closeDone := make(chan error, 1)
				go func() { closeDone <- x.m.ShutdownSession(ctx) }()
				select {
				case err := <-closeDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("global shutdown failed to cancel/join real auth", ctx.Err())
				}
			case "held-token":
				submit(callback)
				select {
				case <-x.tokenStarted:
				case <-ctx.Done():
					t.Fatal("actual token exchange not started")
				}
				abort()
			case "state-mismatch":
				submit(strings.Replace(callback, "state=", "state=wrong-", 1))
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("failed/cancelled authentication succeeded")
				}
				var cancelled mcpauth.AuthenticationCancelledError
				if kind != "state-mismatch" && !errors.As(err, &cancelled) && !errors.Is(err, context.Canceled) {
					t.Fatal("auth cancellation lost type", err)
				}
			case <-ctx.Done():
				t.Fatal("auth worker survived cancellation", ctx.Err())
			}
			submit(callback)
			if err := terminalMCPAuthCallbackListenerClosed(actualListener); err != nil {
				t.Fatal("real callback listener survived completed flow", err)
			}
			connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", x.port), 100*time.Millisecond)
			if err == nil {
				t.Logf("post-completion numeric-port dial is not listener identity: local=%s remote=%s", connection.LocalAddr(), connection.RemoteAddr())
				_ = connection.Close()
				if lsof, lookupErr := exec.LookPath("lsof"); lookupErr == nil {
					probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
					probe := exec.CommandContext(probeCtx, lsof, "-nP", "-iTCP:"+fmt.Sprint(x.port), "-sTCP:LISTEN")
					output, probeErr := probe.CombinedOutput()
					probeCancel()
					t.Logf("actual callback port occupant diagnostic (not a closure assertion): %v %s", probeErr, output)
				}
				t.Log("the captured actual callback listener is closed; a numeric-port connection alone does not identify its owner")
			}
			if kind == "held-token" {
				select {
				case <-x.tokenCancelled:
				case <-ctx.Done():
					t.Fatal("cancelled token HTTP socket survived")
				}
			}
			stored, err := os.ReadFile(filepath.Join(x.f.config, ".credentials.json"))
			if err != nil || bytes.Contains(stored, []byte("private-native-auth-access")) || x.connects.Load() != 0 || x.calls.Load() != 0 {
				t.Fatal("late callback wrote credentials or reconnected", string(stored), x.connects.Load(), err)
			}
		})
	}
}

func TestTerminalMCPAuthenticationCompletedCallbackAllowsPortReuseWithoutLateCredentialWrite(t *testing.T) {
	observed := make(chan net.Listener, 1)
	x := newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{onCallbackListener: func(listener net.Listener) { observed <- listener }})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address, submit, done := startTerminalMCPAuth(t, ctx, x)
	actualListener := terminalMCPAuthObservedListener(t, ctx, observed, x.port)
	callback := terminalMCPAuthCallback(t, address)
	submit(strings.Replace(callback, "state=", "state=wrong-", 1))
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "state mismatch") {
			t.Fatal("actual mismatched callback lost original error", err)
		}
	case <-ctx.Done():
		t.Fatal("actual mismatched callback did not finish", ctx.Err())
	}
	if err := terminalMCPAuthCallbackListenerClosed(actualListener); err != nil {
		t.Fatal("completed actual callback listener was not closed", err)
	}
	// Bind the same OS port to a different real peer. A successful subsequent
	// TCP dial alone cannot identify the completed authentication listener.
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", x.port))
	if err != nil {
		t.Fatal("completed actual callback did not release its port", err)
	}
	replacement := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Private-Callback-Port-Owner", "replacement-loopback-peer")
		w.WriteHeader(http.StatusNoContent)
	}))
	_ = replacement.Listener.Close()
	replacement.Listener = listener
	replacement.Start()
	defer replacement.Close()
	submit(callback)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, callback, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := replacement.Client().Do(request)
	if err != nil {
		t.Fatal("replacement peer did not own the released OS port", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get("X-Private-Callback-Port-Owner") != "replacement-loopback-peer" {
		t.Fatal("late HTTP callback reached the wrong port owner", response.StatusCode, response.Header)
	}
	if err := terminalMCPAuthCallbackListenerClosed(actualListener); err != nil {
		t.Fatal("real port replacement changed the completed callback listener identity", err)
	}
	if err := terminalMCPAuthCallbackListenerClosed(listener.(*net.TCPListener)); err == nil {
		t.Fatal("closure proof accepted the different live replacement peer")
	}
	stored, err := os.ReadFile(filepath.Join(x.f.config, ".credentials.json"))
	if err != nil || bytes.Contains(stored, []byte("private-native-auth-access")) || !bytes.Contains(stored, []byte("private-auth-fixture")) || x.tokens.Load() != 0 || x.connects.Load() != 0 || x.calls.Load() != 0 {
		t.Fatal("late callback after actual port reuse changed the original authentication", string(stored), x.tokens.Load(), x.connects.Load(), x.calls.Load(), err)
	}
}

func TestTerminalMCPAuthenticationRejectsForeignRetainedBorrowedChildAndClosedOwner(t *testing.T) {
	x := newTerminalMCPAuthFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var retained *cooperative.Scope
	if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		retained = scope
		for _, kind := range []string{"not-interactive", "borrowed", "agent-child"} {
			interactive, borrowed, child := x.m.options.Interactive, x.m.mcpBorrowed, x.m.agentChild
			switch kind {
			case "not-interactive":
				x.m.options.Interactive = false
			case "borrowed":
				x.m.mcpBorrowed = true
			case "agent-child":
				x.m.agentChild = true
			}
			_, err := x.m.terminalMCPRemoteTarget(ctx, scope, "auth")
			x.m.options.Interactive, x.m.mcpBorrowed, x.m.agentChild = interactive, borrowed, child
			if !errors.Is(err, cooperative.ErrRestricted) {
				return fmt.Errorf("%s auth capability escaped: %w", kind, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []*cooperative.Scope{nil, retained} {
		if _, err := x.m.terminalMCPRemoteTarget(ctx, scope, "auth"); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("inactive scope admitted OAuth", err)
		}
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := x.m.terminalMCPRemoteTarget(ctx, scope, "auth")
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign scope admitted OAuth: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := x.m.StartTerminalMCPAuthentication(nil, "auth", nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil callback context admitted", err)
	}
	if err := x.m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	if err := x.m.StartTerminalMCPAuthentication(ctx, "auth", nil, nil); err == nil {
		t.Fatal("closed owner began a consent flow")
	}
	if x.tokens.Load() != 0 || x.connects.Load() != 0 || x.models.Load() != 0 {
		t.Fatal("guard rejection performed provider work")
	}
}
