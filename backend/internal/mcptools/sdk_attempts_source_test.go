package mcptools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type sdkAttemptCounts struct {
	Initialize    int `json:"initialize"`
	List          int `json:"list"`
	Call          int `json:"call"`
	Notifications int `json:"notifications"`
}
type sdkAttemptStage struct {
	Label   string                      `json:"label"`
	Counts  map[string]sdkAttemptCounts `json:"counts"`
	Clients []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"clients"`
	Tools []string `json:"tools"`
}
type sdkAttemptSource struct {
	NoticeCounts struct {
		Old     int      `json:"old"`
		Current int      `json:"current"`
		Acks    []string `json:"acks"`
	} `json:"noticeCounts"`
	Stages                           []sdkAttemptStage `json:"stages"`
	CachedToolIdentity               bool              `json:"cachedToolIdentity"`
	CachedToolCallAfterLatestCleanup struct {
		Data json.RawMessage `json:"data"`
	} `json:"cachedToolCallAfterLatestCleanup"`
	Capabilities struct {
		Blocked []string `json:"blocked"`
	} `json:"caps"`
}
type sdkAttemptPeer struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	scanner     *bufio.Scanner
	errors      bytes.Buffer
	counts      sdkAttemptCounts
	rejectFirst bool
}

func sdkAttemptEnvironment(home string) []string {
	return []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=test", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
}
func sdkAttemptCommand(ctx context.Context, home, fixture string, args ...string) *exec.Cmd {
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture}, args...)...)
	cmd.Dir = home
	cmd.Env = sdkAttemptEnvironment(home)
	return cmd
}
func sdkAttemptHome(t *testing.T) string {
	t.Helper()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(home, ".claude"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	return home
}
func sdkAttemptOriginal(t *testing.T, ctx context.Context) sdkAttemptSource {
	t.Helper()
	home := sdkAttemptHome(t)
	fixture, _ := filepath.Abs("../../testdata/native-sdk-mcp-attempts.fixture.ts")
	cmd := sdkAttemptCommand(ctx, home, fixture)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	data, e := cmd.Output()
	if e != nil {
		t.Fatal("actual original SDK nameset lifecycle", e, diagnostics.String())
	}
	var out sdkAttemptSource
	if e = json.Unmarshal(data, &out); e != nil {
		t.Fatal(e, string(data))
	}
	if len(out.Capabilities.Blocked) != 0 || !out.CachedToolIdentity || len(out.Stages) != 6 {
		t.Fatal("Source escaped or changed original producer", string(data))
	}
	return out
}
func sdkAttemptStartPeer(t *testing.T, ctx context.Context, home, name string) *sdkAttemptPeer {
	t.Helper()
	fixture, _ := filepath.Abs("../../testdata/native-sdk-mcp-attempt-peer.fixture.ts")
	p := &sdkAttemptPeer{rejectFirst: name == "retry"}
	p.cmd = sdkAttemptCommand(ctx, home, fixture, name)
	p.cmd.Stderr = &p.errors
	var e error
	p.stdin, e = p.cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	out, e := p.cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	p.scanner = bufio.NewScanner(out)
	if e = p.cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = p.stdin.Close()
		if e := p.cmd.Wait(); e != nil {
			t.Error("actual SDK Server exit", e, p.errors.String())
		}
	})
	return p
}
func (p *sdkAttemptPeer) send(_ context.Context, message protocol.JSONObject) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch message.String("method") {
	case "initialize":
		p.counts.Initialize++
		if p.rejectFirst {
			p.rejectFirst = false
			return nil, errors.New("explicit first peer initialize rejection")
		}
	case "tools/list":
		p.counts.List++
	case "tools/call":
		p.counts.Call++
	default:
		p.counts.Notifications++
	}
	line, e := protocol.EncodeNDJSON(message)
	if e != nil {
		return nil, e
	}
	if _, e = p.stdin.Write(line); e != nil {
		return nil, e
	}
	if !p.scanner.Scan() {
		return nil, fmt.Errorf("physical SDK response ended: %w", p.scanner.Err())
	}
	return append(json.RawMessage(nil), p.scanner.Bytes()...), nil
}
func (p *sdkAttemptPeer) snapshot() sdkAttemptCounts {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts
}
func sdkAttemptConfig(names ...string) protocol.JSONObject {
	out := protocol.NewJSONObject()
	for _, name := range names {
		out.Set(name, raw(map[string]any{"type": "sdk", "name": name}))
	}
	return out
}

func TestActualSDKNamesetRebuildOriginalCacheAndRetainedPhysicalPeers(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("pinned actual Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	expected := sdkAttemptOriginal(t, ctx)
	home := sdkAttemptHome(t)
	peers := map[string]*sdkAttemptPeer{}
	for _, name := range []string{"alpha", "beta", "retry"} {
		peers[name] = sdkAttemptStartPeer(t, ctx, home, name)
	}
	runtime, e := mcpruntime.New(mcpruntime.Options{CWD: home, Version: "fixture", Environment: map[string]string{}, Auth: func(context.Context, string, protocol.JSONObject) (mcpclient.AuthProvider, error) { return nil, nil }, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) { return true, nil }, Authenticate: func(context.Context, string, protocol.JSONObject, func(string) error) error {
		return errors.New("fixture has no auth flow")
	}, ClearOAuth: func(context.Context, string, protocol.JSONObject) error { return nil }, SDK: func(_ context.Context, name string, _ protocol.JSONObject) (*mcpclient.SDKControlBindings, error) {
		return &mcpclient.SDKControlBindings{Send: peers[name].send}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := runtime.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	owner := cooperative.New()
	defer owner.Close()
	var refs ownerasync.NativeReferences
	binding, e := refs.Bind(owner)
	if e != nil {
		t.Fatal(e)
	}
	empty := func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }
	configs, e := mcpconfig.New(mcpconfig.Options{CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), Environment: func(string) (string, bool) { return "", false }, ReadFile: os.ReadFile, SourceEnabled: func(string) bool { return false }, Settings: empty, Policy: empty, Global: empty, Project: empty, Plugins: func(context.Context) (mcpconfig.Result, error) {
		return mcpconfig.Result{Servers: protocol.NewJSONObject()}, nil
	}, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) { return true, nil }, BypassConfirmed: func(context.Context) (bool, error) { return false, nil }})
	if e != nil {
		t.Fatal(e)
	}
	selected := protocol.NewJSONObject()
	service, e := New(Options{Owner: owner, NativeBinding: binding, Runtime: runtime, Configurations: configs, All: func(context.Context) (mcpconfig.Result, error) {
		return mcpconfig.Result{Servers: protocol.NewJSONObject()}, nil
	}, SDKConfigurations: func(context.Context) (protocol.JSONObject, error) { return selected.Clone(), nil }, Environment: map[string]string{}, PermissionContext: func(*cooperative.Scope) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, ScopeForContext: func(v any) *cooperative.Scope { s, _ := v.(*cooperative.Scope); return s }})
	if e != nil {
		t.Fatal(e)
	}
	var originalAlpha, latestAlpha mcpruntime.Connection
	var oldTool queryloop.RegisteredTool
	phases := [][]string{{"alpha"}, {"alpha"}, {"alpha", "beta"}, {"alpha", "beta", "retry"}, {"alpha", "beta", "retry"}, {"beta", "retry"}}
	for i, names := range phases {
		e = owner.Run(ctx, func(scope *cooperative.Scope) error {
			selected = sdkAttemptConfig(names...)
			changed, e := service.SDKNeedsRefresh(scope, selected)
			if e != nil {
				return e
			}
			if changed {
				if e = service.Discover(scope); e != nil {
					return e
				}
			}
			if i == 0 {
				originalAlpha = service.connections[0]
				_, tools, e := service.Catalog(scope)
				if e != nil {
					return e
				}
				oldTool = tools[0]
			}
			if i == 4 {
				latestAlpha = service.connections[0]
			}
			return nil
		})
		if e != nil {
			t.Fatal("phase", i, names, e)
		}
		want := expected.Stages[i]
		actual := map[string]sdkAttemptCounts{}
		for name, peer := range peers {
			actual[name] = peer.snapshot()
		}
		for name, count := range want.Counts {
			got := actual[name]
			if got.Initialize != count.Initialize || got.List != count.List || got.Notifications != count.Notifications {
				t.Fatalf("original %s %s handshake/list/notification mismatch: Go=%+v Source=%+v", want.Label, name, got, count)
			}
		}
		if i == 2 && originalAlpha.Client == service.connections[0].Client {
			t.Fatal("Source setup rebuilt same-name SDK client; Go reused old transport")
		}
	}
	foreignOwner := cooperative.New()
	e = foreignOwner.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := oldTool.CallJSON(ctx, object(raw(map[string]string{"text": "FOREIGN_MUST_NOT_WRITE"})), queryloop.JSONToolCallContext{Opaque: scope})
		return err
	})
	foreignOwner.Close()
	if !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("foreign Owner executed cached SDK tool", e)
	}
	if latestAlpha.Client == originalAlpha.Client {
		t.Fatal("Source latest and original peer identity must differ")
	}
	if _, e = originalAlpha.Client.CallTool(ctx, "physical", object(raw(map[string]string{"text": "OLD_ALPHA_SURVIVES_NAME_REMOVAL"})), protocol.NewJSONObject(), mcpclient.RequestOptions{}); e != nil {
		t.Fatal("Source retained older physical peer after removal", e)
	}
	if _, e = latestAlpha.Client.CallTool(ctx, "physical", object(raw(map[string]string{"text": "SHOULD_NOT_WRITE"})), protocol.NewJSONObject(), mcpclient.RequestOptions{}); e == nil {
		t.Fatal("removed current peer was not closed")
	}
	for _, peer := range service.connections {
		if e = runtime.CloseConnection(ctx, peer); e != nil {
			t.Fatal(e)
		}
	}
	var result queryloop.ToolCallResult
	e = owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		result, e = oldTool.CallJSON(ctx, object(raw(map[string]string{"text": "CACHED_SOURCE_TOOL_SURVIVES_REBUILD"})), queryloop.JSONToolCallContext{Opaque: scope})
		return e
	})
	if e != nil {
		t.Fatal("actual original cached Tool closure lost its physical peer", e)
	}
	var got, want any
	_ = json.Unmarshal(result.Data, &got)
	_ = json.Unmarshal(expected.CachedToolCallAfterLatestCleanup.Data, &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actual Source Tool.call result: Go %s Source %s", result.Data, expected.CachedToolCallAfterLatestCleanup.Data)
	}
	physical, e := os.ReadFile(filepath.Join(home, "alpha.txt"))
	if e != nil || string(physical) != "CACHED_SOURCE_TOOL_SURVIVES_REBUILD" {
		t.Fatal("physical cached consumer", string(physical), e)
	}
	if e = runtime.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = originalAlpha.Client.CallTool(ctx, "physical", protocol.NewJSONObject(), protocol.NewJSONObject(), mcpclient.RequestOptions{}); e == nil {
		t.Fatal("root graph Close retained superseded SDK peer")
	}
}
