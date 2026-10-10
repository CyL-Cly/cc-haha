package clibootstrap

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func agentsNativeFixture(t *testing.T) *NativeBoundary {
	t.Helper()
	root, e := os.MkdirTemp("", "cc-haha-agents-bootstrap-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	home, cwd, tmp := filepath.Join(root, "home"), filepath.Join(root, "workspace"), filepath.Join(root, "tmp")
	for _, p := range []string{home, cwd, tmp, filepath.Join(cwd, ".git")} {
		if e = os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": tmp, "USER_TYPE": "external", "NODE_ENV": "production", "TZ": "UTC", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH": "1"}
	n, e := NewOfflineAgentsNativeBoundary(env, cwd)
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func agentsWrite(t *testing.T, p, content string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestAgentsParserContractsAndHostProfiles(t *testing.T) {
	for _, c := range []struct {
		args    []string
		handled bool
		exit    int
		needle  string
	}{{[]string{"agents"}, true, 86, ""}, {[]string{"agents", "--help"}, true, 0, "Usage: claude agents"}, {[]string{"agents", "list"}, true, 1, "too many arguments"}, {[]string{"agents", "--unknown"}, true, 1, "unknown option"}, {[]string{"agents", "--setting-sources"}, true, 1, "argument missing"}, {[]string{"agents", "--setting-sources", "wrong", "--help"}, true, 1, "Invalid setting source"}, {[]string{"mcp", "list"}, false, 0, ""}, {[]string{"--print", "agents"}, false, 0, ""}, {[]string{"--bare", "agents"}, true, 86, ""}} {
		r, h := AgentsArguments(c.args, "test-version")
		if h != c.handled || h && (r.ExitCode != c.exit || !strings.Contains(r.Stdout+r.Stderr, c.needle)) {
			t.Errorf("args=%v handled=%v result=%+v", c.args, h, r)
		}
	}
}
func TestMainAgentsUsesSameSettingsAndOwnerReadOnly(t *testing.T) {
	n := agentsNativeFixture(t)
	config := n.Environment["CLAUDE_CONFIG_DIR"]
	agentsWrite(t, filepath.Join(config, "agents/read.md"), "---\nname: Reader\ndescription: real private file\nmodel: custom-model\nmemory: user\n---\nprivate prompt")
	agentsWrite(t, filepath.Join(config, "settings.json"), `{"builtInAgentOverrides":{"Explore":{"model":" override-model ","future":1}},"futureRoot":true}`)
	var out strings.Builder
	m, e := NewMainAgents([]string{"agents"}, n, os.Getpid(), true, &out)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if m.Catalog == nil || m.Owner == nil || m.Startup == nil || m.Global == nil {
		t.Fatal("missing existing shared kernel bindings")
	}
	code, e := m.Run(context.Background())
	if code != 0 || e != nil || !strings.Contains(out.String(), "Reader · custom-model · user memory") || !strings.Contains(out.String(), "Explore · override-model") {
		t.Fatalf("code=%d error=%v stdout=%q", code, e, out.String())
	}
	if n.Environment["CLAUDE_CODE_ENTRYPOINT"] != "sdk-cli" {
		t.Fatal("noninteractive host")
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if e = foreign.Run(context.Background(), func(s *cooperative.Scope) error { _, e := m.Catalog.List(s); return e }); e == nil {
		t.Fatal("second owner admitted")
	}
	// Native wrapper denies reads outside the private allocation and symlink ancestry;
	// managed absence is an explicit external fixture, never a real-machine read.
	files := mainAgentsFiles{n}
	outside := filepath.Join(filepath.Dir(n.Home), "sentinel")
	agentsWrite(t, outside, "sentinel")
	for _, read := range []func() error{func() error { _, e := files.Stat(outside); return e }, func() error { _, e := files.Lstat(outside); return e }, func() error { _, e := files.ReadFile(outside); return e }, func() error { _, e := files.ReadDir(outside); return e }, func() error { _, e := files.Realpath(outside); return e }} {
		if e := read(); e == nil {
			t.Fatal("outside read admitted")
		}
	}
	managed := filepath.Join(n.ManagedRoot, ".claude/agents")
	if _, e = files.Stat(managed); !os.IsNotExist(e) {
		t.Fatalf("managed snapshot=%v", e)
	}
	link := filepath.Join(n.CWD, "link")
	if e = os.Symlink(config, link); e != nil {
		t.Fatal(e)
	}
	if _, e = files.Realpath(link); e == nil {
		t.Fatal("symlink ancestry admitted")
	}
	info, e := files.Lstat(filepath.Join(config, "agents/read.md"))
	if e != nil || files.Identity(info) == "" {
		t.Fatalf("identity=%v", e)
	}
	before, e := os.ReadFile(filepath.Join(config, "settings.json"))
	if e != nil || !strings.Contains(string(before), "futureRoot") {
		t.Fatal("user settings altered")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("closed output") }
func TestMainAgentsIOCancellationAndNecessaryCapabilities(t *testing.T) {
	if _, e := NewMainAgents(nil, nil, 1, true, io.Discard); e == nil {
		t.Fatal("nil native admitted")
	}
	n := agentsNativeFixture(t)
	if _, e := NewMainAgents([]string{"agents"}, n, 0, true, io.Discard); e == nil {
		t.Fatal("invalid process")
	}
	if _, e := NewMainAgents([]string{"agents"}, n, 1, true, nil); e == nil {
		t.Fatal("nil stdio")
	}
	if _, e := NewMainAgents([]string{"agents", "--help"}, n, 1, true, io.Discard); e == nil {
		t.Fatal("handler for help")
	}
	for _, c := range []struct {
		args                     []string
		envkey, envvalue, needle string
	}{{[]string{"--plugin-dir", "private", "agents"}, "", "", "startup flag"}, {[]string{"agents"}, "CLAUDE_CODE_SYNC_PLUGIN_INSTALL", "true", "plugin install sync"}, {[]string{"agents"}, "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH", "0", "remote eligibility"}, {[]string{"agents", "--setting-sources", "invalid"}, "", "", "Invalid setting source"}} {
		p := agentsNativeFixture(t)
		if c.envkey != "" {
			p.Environment[c.envkey] = c.envvalue
		}
		_, e := NewMainAgents(c.args, p, os.Getpid(), true, io.Discard)
		if e == nil || !strings.Contains(e.Error(), c.needle) {
			t.Errorf("args=%v err=%v", c.args, e)
		}
	}
	n = agentsNativeFixture(t)
	m, e := NewMainAgents([]string{"agents"}, n, os.Getpid(), true, failedWriter{})
	if e != nil {
		t.Fatal(e)
	}
	code, e := m.Run(context.Background())
	m.Close()
	if code != 1 || e == nil || !strings.Contains(e.Error(), "closed output") {
		t.Fatalf("io result=%d %v", code, e)
	}
	n = agentsNativeFixture(t)
	m, e = NewMainAgents([]string{"agents"}, n, os.Getpid(), false, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	if n.Environment["CLAUDE_CODE_ENTRYPOINT"] != "cli" {
		t.Fatal("interactive incorrectly SDK")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, e = m.Run(ctx)
	m.Close()
	if code != 1 || e == nil {
		t.Fatal("cancelled input admitted")
	}
	m.Close()
	(*MainAgents)(nil).Close()
	n = agentsNativeFixture(t)
	agentsWrite(t, filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "plugins/installed_plugins.json"), `{}`)
	m, e = NewMainAgents([]string{"agents"}, n, os.Getpid(), true, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	code, e = m.Run(context.Background())
	m.Close()
	if code != 1 || e == nil || !strings.Contains(e.Error(), "plugin cache") {
		t.Fatalf("plugin=%d %v", code, e)
	}
	n = agentsNativeFixture(t)
	agentsWrite(t, filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), `{"model":"own-model","future":1}`)
	m, e = NewMainAgents([]string{"--bare", "agents"}, n, os.Getpid(), true, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	code, e = m.Run(context.Background())
	m.Close()
	if code != 0 || e != nil || n.Environment["CLAUDE_CODE_SIMPLE"] != "1" {
		t.Fatal("Source bare builtin only")
	}
}
func TestOfflineAgentsCanonicalNativeGuards(t *testing.T) {
	n := agentsNativeFixture(t)
	original := n.Environment
	clone := func() map[string]string {
		v := map[string]string{}
		for k, x := range original {
			v[k] = x
		}
		return v
	}
	for _, c := range []struct{ key, value string }{{"HOME", "relative"}, {"USERPROFILE", "wrong"}, {"CLAUDE_CONFIG_DIR", "wrong"}, {"TMPDIR", "wrong"}, {"USER_TYPE", "ant"}, {"NODE_ENV", "development"}, {"TZ", "other"}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "0"}, {"ANTHROPIC_API_KEY", "not-allowed"}} {
		v := clone()
		v[c.key] = c.value
		if _, e := NewOfflineAgentsNativeBoundary(v, n.CWD); e == nil {
			t.Errorf("unsafe %s accepted", c.key)
		}
	}
	if _, e := NewOfflineAgentsNativeBoundary(clone(), n.CWD+"/missing"); e == nil {
		t.Fatal("wrong cwd")
	}
	tmp := n.Temp
	if e := os.Remove(tmp); e != nil {
		t.Fatal(e)
	}
	if _, e := NewOfflineAgentsNativeBoundary(clone(), n.CWD); e == nil {
		t.Fatal("missing canonical allocation")
	}
}

func TestMainAgentsExplicitSettingsPrivateProvenance(t *testing.T) {
	n := agentsNativeFixture(t)
	outside := filepath.Join(filepath.Dir(n.Home), "outside.json")
	agentsWrite(t, outside, `{"builtInAgentOverrides":{"Explore":{"model":"outside"}}}`)
	if _, e := NewMainAgents([]string{"--settings", outside, "agents"}, n, os.Getpid(), true, io.Discard); e == nil || !strings.Contains(e.Error(), "guard denied") {
		t.Fatalf("outside=%v", e)
	}
	inside := filepath.Join(n.CWD, "flag.json")
	agentsWrite(t, inside, `{"builtInAgentOverrides":{"Explore":{"model":"flag-model"}}}`)
	var out strings.Builder
	m, e := NewMainAgents([]string{"--settings", "flag.json", "agents", "--setting-sources", ""}, n, os.Getpid(), true, &out)
	if e != nil {
		t.Fatal(e)
	}
	code, e := m.Run(context.Background())
	m.Close()
	if code != 0 || e != nil || !strings.Contains(out.String(), "Explore · flag-model") {
		t.Fatalf("flag result=%d %v %q", code, e, out.String())
	}
	n = agentsNativeFixture(t)
	out.Reset()
	m, e = NewMainAgents([]string{"--settings", `{"builtInAgentOverrides":{"Plan":{"model":"inline"}}}`, "agents"}, n, os.Getpid(), true, &out)
	if e != nil {
		t.Fatal(e)
	}
	code, e = m.Run(context.Background())
	m.Close()
	if code != 0 || e != nil || !strings.Contains(out.String(), "Plan · inline") {
		t.Fatalf("inline=%d %v %q", code, e, out.String())
	}
}

func TestAgentsCloseStopsSharedGlobalNativeResource(t *testing.T) {
	n := agentsNativeFixture(t)
	config := n.Environment["CLAUDE_CONFIG_DIR"]
	started, stopped := false, false
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: n.Home, ConfigDir: &config, Now: n.Now, NowMillis: n.NowMillis, PID: os.Getpid(), FS: mainGlobalFiles{n: n}, Watcher: func(string, time.Duration, func(cliglobalconfig.FileStat)) func() {
		started = true
		return func() { stopped = true }
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer global.Close()
	if e = global.Enable(); e != nil {
		t.Fatal(e)
	}
	if _, e = global.Get(); e != nil {
		t.Fatal(e)
	}
	if !started {
		t.Fatal("real shared config cache did not open the native resource")
	}
	m := &MainAgents{Owner: cooperative.New(), Global: global}
	m.Close()
	if !stopped {
		t.Fatal("main agents leaked its existing shared Global watcher after Close")
	}
	m.Close()
}
