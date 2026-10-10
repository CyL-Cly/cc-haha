package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestNativeSandboxActualMainPrintTerminalWrapPolicyAndShutdown(t *testing.T) {
	if os.Getenv("CC_HAHA_SANDBOX_PHYSICAL_BROKER") != "1" {
		t.Skip("dedicated nonnested actual Seatbelt broker required")
	}
	f := newNativePluginFixture(t)
	f.n.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["SHELL"] = "/bin/bash"
	f.write(t, filepath.Join(f.config, "settings.json"), `{"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false,"network":{"allowedDomains":[]}},"future":{"keep":true}}`)
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 100, Tools: []string{"Bash", "Read"}, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if m.sandbox == nil || m.sandbox.manager == nil || !m.initialized {
		t.Fatal("actual MainPrint did not own initialized native runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8_000_000_000)
	defer cancel()
	r, e := m.RunTerminalBash(ctx, "printf 'ordinary shared Harness bytes' > allowed.txt; cat allowed.txt", nil)
	if e != nil || !strings.Contains(r.Stdout, "ordinary shared Harness bytes") {
		t.Fatal("actual terminal execution", r, e)
	}
	body, e := os.ReadFile(filepath.Join(f.cwd, "allowed.txt"))
	if e != nil || string(body) != "ordinary shared Harness bytes" {
		t.Fatal(e, string(body))
	}
	outside := filepath.Join(f.home, "outside.txt")
	r, e = m.RunTerminalBash(ctx, "printf forbidden > "+bashSandboxQuote(outside), nil)
	if e != nil || !strings.Contains(r.Stderr, "Operation not permitted") {
		t.Fatal("policy-required terminal override failed", r, e)
	}
	if _, e = os.Stat(outside); !os.IsNotExist(e) {
		t.Fatal("terminal write escaped", e)
	}
	http, socks := m.sandbox.manager.ProxyPorts()
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, port := range []int{int(http), int(socks)} {
		c, e := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if e == nil {
			c.Close()
			t.Fatal("native proxy survived actual MainPrint shutdown", port)
		}
	}
}

func TestNativeSandboxActualMainPrintNetworkPermissionUsesExistingOwnerFuture(t *testing.T) {
	if os.Getenv("CC_HAHA_SANDBOX_PHYSICAL_BROKER") != "1" {
		t.Skip("dedicated nonnested actual Seatbelt broker required")
	}
	f := newNativePluginFixture(t)
	f.n.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
	f.n.Environment["SHELL"] = "/bin/bash"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.write(t, filepath.Join(f.config, "settings.json"), `{"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false,"network":{"allowedDomains":[]}}}`)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("approved native network bytes")) }))
	defer peer.Close()
	type ask struct {
		tool   string
		input  protocol.JSONObject
		call   queryloop.JSONToolCallContext
		future *sdkhost.OwnerFuture[bool]
	}
	asks := make(chan ask, 2)
	var m *MainPrint
	resolve := func(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (*sdkhost.OwnerFuture[bool], error) {
		future, e := sdkhost.NewOwnerFuture[bool](m.Owner)
		if e != nil {
			return nil, e
		}
		asks <- ask{tool.Name, input.Clone(), call, future}
		settled := future.Done()
		go func() {
			select {
			case <-ctx.Done():
				_, _ = m.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error { return future.Reject(next, ctx.Err()) })
			case <-settled:
			}
		}()
		return future, nil
	}
	disabled := false
	var e error
	m, e = NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 100, Tools: []string{"Bash"}, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Interactive: true, StartSandboxNetworkPermission: resolve, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, allowed := range []bool{true, false} {
		done := make(chan error, 1)
		go func() {
			result, e := m.RunTerminalBash(context.Background(), "/usr/bin/curl --fail --silent --show-error --noproxy '' "+peer.URL, nil)
			if e == nil && allowed && !strings.Contains(result.Stdout, "approved native network bytes") {
				e = fmt.Errorf("approved result %v", result)
			}
			done <- e
		}()
		var request ask
		select {
		case request = <-asks:
		case <-time.After(8 * time.Second):
			t.Fatal("existing permission request never arrived")
		}
		if request.tool != "SandboxNetworkAccess" || request.input.String("host") != "127.0.0.1" || len(request.input.OwnProperties()) != 1 || request.call.ToolUseID == nil || request.call.PermDecision.Description == nil || string(request.call.PermDecision.Description) != `"Allow network connection to 127.0.0.1?"` {
			t.Fatal("Source synthetic request fields changed", request)
		}
		select {
		case e := <-done:
			t.Fatal("command escaped before actual permission future", e)
		default:
		}
		receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Input, func(scope *cooperative.Scope) error { return request.future.Resolve(scope, allowed) })
		if e != nil || receipt.Wait() != nil {
			t.Fatal(e)
		}
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("approved/denied command failed to join")
		}
	}
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func bashSandboxQuote(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\\''") + "'" }
