package workflowcommands

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type testHost struct {
	owner             *cooperative.Owner
	state             *bootstrapstate.State
	env               *clistartupenv.Environment
	cache             *clisettings.State
	service           *Service
	home, config, cwd string
	diagnostics       []string
}

func newHost(t *testing.T) *testHost {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	cwd := filepath.Join(home, "repo", "nested")
	_ = os.MkdirAll(config, 0777)
	_ = os.MkdirAll(cwd, 0777)
	_ = os.MkdirAll(filepath.Join(home, "repo", ".git"), 0777)
	owner := cooperative.New()
	t.Cleanup(func() { owner.Close() })
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: time.Now, PID: os.Getpid(), Watcher: func(string, time.Duration, func(cliglobalconfig.FileStat)) func() { return func() {} }})
	if e != nil {
		t.Error(e)
	}
	t.Cleanup(func() { _ = global.Close() })
	if e = global.Enable(); e != nil {
		t.Error(e)
	}
	cache, _ := clisettings.NewState(func() float64 { return 1000 })
	settings, e := clisettings.NewService(clisettings.Options{State: cache, ConfigHome: config, OriginalCWD: cwd, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1000 }, PID: os.Getpid()})
	if e != nil {
		t.Error(e)
	}
	env, _ := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "USER_TYPE", Value: quote("external")}))
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settings, SettingsState: cache, Environment: env, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if e != nil {
		t.Error(e)
	}
	gate, e := NewGate(owner, startup, env)
	if e != nil {
		t.Error(e)
	}
	state, _ := bootstrapstate.New(owner, "00000000-0000-4000-8000-000000000001", cwd, func() (string, error) { return "", errors.New("unused UUID") })
	resolver, _ := bootstrapstate.NewConfigResolver(owner, bootstrapstate.ConfigHost{Environment: func(string) (string, bool) { return config, true }, HomeDir: func() (string, error) { return home, nil }})
	git, e := gitbranch.New(gitbranch.Options{Owner: owner, CWD: func() string { return state.Snapshot().OriginalCWD }, Now: func() int64 { return 1000 }, StatSync: os.Stat, Stat: os.Stat, ReadFile: os.ReadFile, WatchFile: (&gitbranch.NativePollWatch{Stat: os.Stat}).Watch, ScrollDraining: func() bool { return false }, RegisterCleanup: func(func(*cooperative.Scope) error) {
		panic("FindRoot-only Workflow component must not initialize branch watchers")
	}})
	if e != nil {
		t.Error(e)
	}
	host := &testHost{owner: owner, state: state, env: env, cache: cache, home: home, config: config, cwd: cwd}
	if e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_ = state.SetProjectRoot(scope, filepath.Join(home, "repo"))
		host.service, e = New(scope, Options{Owner: owner, State: state, Config: resolver, Gate: gate, Git: git, Home: home, Diagnostic: func(_ *cooperative.Scope, s string) { host.diagnostics = append(host.diagnostics, s) }})
		return e
	}); e != nil {
		t.Error(e)
	}
	return host
}
func (h *testHost) run(t *testing.T, fn func(*cooperative.Scope) error) {
	t.Helper()
	if e := h.owner.Run(context.Background(), fn); e != nil {
		t.Error(e)
	}
}
func writeWorkflow(t *testing.T, p, name, desc string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(p), 0777)
	if e := os.WriteFile(p, []byte("export const meta={name:"+string(quote(name))+",description:"+string(quote(desc))+"};\nthrow new Error('must not execute');"), 0666); e != nil {
		t.Error(e)
	}
}
func names(commands []*Command) []string {
	out := []string{}
	for _, c := range commands {
		out = append(out, c.Name())
	}
	return out
}
func TestFreshCacheShadowAndLiveGate(t *testing.T) {
	h := newHost(t)
	file := filepath.Join(h.config, "workflows", "a.js")
	writeWorkflow(t, file, "saved", "before")
	writeWorkflow(t, filepath.Join(h.home, "repo", ".claude", "workflows", "b.js"), "saved", "parent")
	writeWorkflow(t, filepath.Join(h.cwd, ".claude", "workflows", "c.js"), "saved", "closest")
	h.run(t, func(s *cooperative.Scope) error {
		f, e := h.service.BeginCommands(s, h.cwd)
		if e != nil {
			return e
		}
		before, e := f.Await(s)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(names(before), []string{"deep-research", "saved"}) {
			t.Error(names(before))
		}
		d, _ := before[1].Descriptor(s)
		if string(d.Get("description")) != `"closest"` {
			t.Error(d)
		}
		writeWorkflow(t, filepath.Join(h.cwd, ".claude", "workflows", "c.js"), "saved", "after")
		cached, _ := h.service.BeginCommands(s, h.cwd)
		if cached != f {
			t.Error("promise cache changed")
		}
		again, e := cached.Await(s)
		if e != nil {
			return e
		}
		if again[1] != before[1] {
			t.Error("command identity changed")
		}
		fresh, _ := h.service.BeginFreshCommands(s, nil)
		values, e := fresh.Await(s)
		if e != nil {
			return e
		}
		d, _ = values[1].Descriptor(s)
		if string(d.Get("description")) != `"after"` {
			t.Error(d)
		}
		_ = h.env.SetProperty(quote("CLAUDE_CODE_DISABLE_WORKFLOWS"), quote("\ufeffYES\ufeff"))
		enabled, e := before[1].IsEnabled(s)
		if e != nil || enabled {
			t.Error(enabled, e)
		}
		reason, e := h.service.options.Gate.DisabledReason(s)
		if reason != "env" {
			t.Error(reason, e)
		}
		_ = h.env.SetProperty(quote("CLAUDE_CODE_DISABLE_WORKFLOWS"), quote("0"))
		if e = h.service.ClearCommandCache(s); e != nil {
			return e
		}
		if e = h.service.ResetBundledForTesting(s); e != nil {
			return e
		}
		return nil
	})
}
func TestSettingsBooleanCacheAndInvalidWholeFile(t *testing.T) {
	h := newHost(t)
	path := filepath.Join(h.config, "settings.json")
	for _, row := range []struct{ raw, reason string }{{`{"disableWorkflows":true}`, "settings"}, {`{"enableWorkflows":false}`, "settings"}, {`{"disableWorkflows":false,"enableWorkflows":true}`, ""}, {`{"disableWorkflows":3}`, ""}, {`{"enableWorkflows":null}`, ""}} {
		if e := os.WriteFile(path, []byte(row.raw), 0666); e != nil {
			t.Error(e)
		}
		h.cache.ResetSettingsCache()
		h.run(t, func(s *cooperative.Scope) error {
			reason, e := h.service.options.Gate.DisabledReason(s)
			if e != nil {
				return e
			}
			if reason != row.reason {
				t.Error(row.raw, reason)
			}
			return nil
		})
	}
}
func TestDisabledCacheRemainsEmptyUntilExplicitClear(t *testing.T) {
	h := newHost(t)
	h.run(t, func(s *cooperative.Scope) error {
		_ = h.env.SetProperty(quote("CLAUDE_CODE_DISABLE_WORKFLOWS"), quote("true"))
		f, _ := h.service.BeginCommands(s, h.cwd)
		v, e := f.Await(s)
		if e != nil {
			return e
		}
		if len(v) != 0 {
			t.Error(v)
		}
		_ = h.env.SetProperty(quote("CLAUDE_CODE_DISABLE_WORKFLOWS"), quote("false"))
		g, _ := h.service.BeginCommands(s, h.cwd)
		if g != f {
			t.Error("disabled memo lost")
		}
		_ = h.service.ClearCommandCache(s)
		g, _ = h.service.BeginCommands(s, h.cwd)
		v, e = g.Await(s)
		if e != nil {
			return e
		}
		if len(v) != 1 {
			t.Error(v)
		}
		return nil
	})
}
func TestDefinitionIdentityAndPromptUTF16(t *testing.T) {
	h := newHost(t)
	h.run(t, func(s *cooperative.Scope) error {
		raw := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote("before")}, protocol.JSONField{Name: "description", Value: []byte(`"🚀\ud800"`)}, protocol.JSONField{Name: "source", Value: quote("plugin")})
		c, e := NewCommand(s, h.service.options.Gate, Definition{raw})
		if e != nil {
			return e
		}
		d, _ := c.Descriptor(s)
		if string(d.Get("contentLength")) != "203" || string(d.Get("source")) != `"plugin"` {
			t.Error(d)
		}
		raw.Set("name", quote("after"))
		name, e := c.UserFacingName(s)
		if name != "after" || e != nil {
			t.Error(name, e)
		}
		if c.Name() != "before" {
			t.Error(c.Name())
		}
		for _, arg := range []json.RawMessage{[]byte(`"\ufeff "`), []byte(`"\u0085"`), []byte(`"  🚀\ud800  "`)} {
			f, e := c.BeginPrompt(s, arg)
			if e != nil {
				return e
			}
			output, e := f.Await(s)
			if e != nil {
				return e
			}
			var block []map[string]string
			if e = json.Unmarshal(output, &block); e != nil {
				return e
			}
			if len(block) != 1 || block[0]["type"] != "text" {
				t.Error(block)
			}
		}
		keys := c.Keys()
		keys[0] = "changed"
		if c.Keys()[0] != "type" {
			t.Error("exposed mutable key slice")
		}
		return nil
	})
}
func TestOwnerAndOriginalCapabilities(t *testing.T) {
	h := newHost(t)
	if _, e := New(nil, Options{}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	if _, e := NewGate(nil, nil, nil); e == nil {
		t.Error("missing gate")
	}
	if _, e := NewCommand(nil, h.service.options.Gate, Definition{}); e == nil {
		t.Error("outside owner")
	}
	for _, method := range []func(*cooperative.Scope) error{h.service.ClearCommandCache, h.service.ResetBundledForTesting, h.service.check} {
		if e := method(nil); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
	}
	h.run(t, func(s *cooperative.Scope) error {
		for _, row := range []string{`{}`, `{"name":"bad:name","description":"d"}`, `{"name":3,"description":"d"}`, `{"name":"valid","description":3}`} {
			var o protocol.JSONObject
			_ = o.UnmarshalJSON([]byte(row))
			if _, e := NewCommand(s, h.service.options.Gate, Definition{o}); e == nil {
				t.Error("unsupported original accepted", row)
			}
		}
		o := h.service.options
		o.OriginalInlinePlugins = []string{"private-plugin"}
		if _, e := New(nil, o); e == nil {
			t.Error("plugin side effects accepted")
		}
		return nil
	})
}

type nativeFaultFS struct {
	OSFileSystem
	readDir, stat, read bool
}

func (f nativeFaultFS) ReadDir(p string) ([]Entry, error) {
	if f.readDir {
		return nil, &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrPermission}
	}
	return f.OSFileSystem.ReadDir(p)
}
func (f nativeFaultFS) Stat(p string) (fs.FileInfo, error) {
	if f.stat {
		return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrPermission}
	}
	return f.OSFileSystem.Stat(p)
}
func (f nativeFaultFS) ReadFile(p string) ([]byte, error) {
	if f.read {
		return nil, &fs.PathError{Op: "read", Path: p, Err: fs.ErrPermission}
	}
	return f.OSFileSystem.ReadFile(p)
}
func TestDirectoryFaultsKeepRemainingAndNoScriptExecution(t *testing.T) {
	for _, fault := range []nativeFaultFS{{readDir: true}, {stat: true}, {read: true}} {
		h := newHost(t)
		writeWorkflow(t, filepath.Join(h.config, "workflows", "a.js"), "valid", "private")
		h.service.options.FS = fault
		h.run(t, func(s *cooperative.Scope) error {
			f, e := h.service.BeginFreshCommands(s, nil)
			if e != nil {
				return e
			}
			v, e := f.Await(s)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(names(v), []string{"deep-research"}) {
				t.Error(names(v))
			}
			return nil
		})
	}
}
func TestDirectorySemanticFailuresAndSymlinkNativeEntries(t *testing.T) {
	h := newHost(t)
	directory := filepath.Join(h.config, "workflows")
	writeWorkflow(t, filepath.Join(directory, "a.js"), "valid", "d")
	_ = os.WriteFile(filepath.Join(directory, "bad.js"), []byte("export const meta={name:'bad:name',description:'d'};"), 0666)
	_ = os.WriteFile(filepath.Join(directory, "too.js"), make([]byte, MaxScriptBytes+1), 0666)
	_ = os.WriteFile(filepath.Join(directory, "ignore.txt"), []byte("not JS"), 0666)
	_ = os.Mkdir(filepath.Join(directory, "dir.js"), 0777)
	_ = os.Symlink(filepath.Join(directory, "a.js"), filepath.Join(directory, "link.js"))
	h.run(t, func(s *cooperative.Scope) error {
		f, e := h.service.BeginFreshCommands(s, nil)
		if e != nil {
			return e
		}
		v, e := f.Await(s)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(names(v), []string{"deep-research", "valid"}) {
			t.Error(names(v))
		}
		if len(h.diagnostics) < 2 {
			t.Error(h.diagnostics)
		}
		return nil
	})
}

func TestCachePreservesInvalidSourceResultUntilClear(t *testing.T) {
	h := newHost(t)
	file := filepath.Join(h.config, "workflows", "bad.js")
	_ = os.MkdirAll(filepath.Dir(file), 0777)
	_ = os.WriteFile(file, []byte("export const meta={name:'broken',description:};"), 0666)
	h.run(t, func(scope *cooperative.Scope) error {
		f, e := h.service.BeginCommands(scope, h.cwd)
		if e != nil {
			return e
		}
		initial, e := f.Await(scope)
		if e != nil || len(initial) != 1 || len(h.diagnostics) != 1 {
			t.Error(initial, h.diagnostics, e)
		}
		writeWorkflow(t, file, "fixed", "fixed")
		again, _ := h.service.BeginCommands(scope, h.cwd)
		if again != f {
			t.Error("rejected memo replaced")
		}
		retained, e := again.Await(scope)
		if e != nil || len(retained) != 1 {
			t.Error(retained, e)
		}
		_ = h.service.ClearCommandCache(scope)
		next, _ := h.service.BeginCommands(scope, h.cwd)
		values, e := next.Await(scope)
		if e != nil {
			return e
		}
		if len(values) != 2 {
			t.Error(names(values))
		}
		return nil
	})
}
func TestForeignConfigAndWorktreeFailBeforeMetadata(t *testing.T) {
	h := newHost(t)
	if e := os.Remove(filepath.Join(h.home, "repo", ".git")); e != nil {
		t.Error(e)
	}
	if e := os.WriteFile(filepath.Join(h.home, "repo", ".git"), []byte("gitdir: outside-unverified"), 0666); e != nil {
		t.Error(e)
	}
	h.run(t, func(scope *cooperative.Scope) error {
		f, e := h.service.BeginDefinitions(scope, nil)
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		var cap *CapabilityError
		if !errors.As(e, &cap) {
			t.Error("worktree validation approximated", e)
		}
		return nil
	})
}
func TestConstructorAndMethodOwnerBoundaries(t *testing.T) {
	h := newHost(t)
	other := cooperative.New()
	defer other.Close()
	if e := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := h.service.BeginDefinitions(scope, nil); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if _, e := h.service.BeginCommands(scope, h.cwd); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if _, e := h.service.BeginFreshCommands(scope, nil); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if _, e := h.service.options.Gate.DisabledReason(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		return nil
	}); e != nil {
		t.Error(e)
	}
	h.run(t, func(scope *cooperative.Scope) error {
		raw := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote("ordinary")}, protocol.JSONField{Name: "description", Value: quote("desc")})
		c, e := NewCommand(scope, h.service.options.Gate, Definition{raw})
		if e != nil {
			return e
		}
		if _, e = c.Descriptor(nil); e == nil {
			t.Error("descriptor escaped owner")
		}
		if _, e = c.UserFacingName(nil); e == nil {
			t.Error("closure escaped owner")
		}
		if _, e = c.IsEnabled(nil); e == nil {
			t.Error("gate escaped owner")
		}
		if _, e = c.BeginPrompt(nil, quote("args")); e == nil {
			t.Error("prompt escaped owner")
		}
		if _, e = c.BeginPrompt(scope, []byte("null")); e == nil {
			t.Error("non-string input accepted")
		}
		raw.Set("name", []byte("null"))
		if _, e = c.UserFacingName(scope); e == nil {
			t.Error("late unsupported name accepted")
		}
		raw.Set("name", quote("ordinary"))
		_ = h.env.SetProperty(quote("CLAUDE_CODE_DISABLE_WORKFLOWS"), []byte("true"))
		if reason, e := h.service.options.Gate.DisabledReason(scope); e != nil || reason != "env" {
			t.Error("original boolean env must preserve isEnvTruthy semantics", reason, e)
		}
		return nil
	})
}

// Original cached settings are the same mutable settingsCache objects; this
// proves gate consumption without pretending an active policy file was loaded.
func TestCachedPolicyGateAndHostChangeRejection(t *testing.T) {
	h := newHost(t)
	h.run(t, func(scope *cooperative.Scope) error {
		policy := protocol.NewJSONObject(protocol.JSONField{Name: "disableWorkflows", Value: []byte("true")})
		h.cache.SetCachedSettingsForSource("policySettings", &policy)
		if reason, e := h.service.options.Gate.DisabledReason(scope); e != nil || reason != "managed" {
			t.Error(reason, e)
		}
		policy.Set("disableWorkflows", []byte("false"))
		for _, raw := range []string{"false", "null", "0", `""`, `"not-truthy"`} {
			_ = h.env.SetProperty(quote("CLAUDE_CODE_DISABLE_WORKFLOWS"), []byte(raw))
			if reason, e := h.service.options.Gate.DisabledReason(scope); e != nil || reason != "" {
				t.Error(raw, reason, e)
			}
		}
		_ = h.env.SetProperty(quote("CLAUDE_CODE_DISABLE_WORKFLOWS"), []byte("3"))
		if _, e := h.service.options.Gate.DisabledReason(scope); e == nil {
			t.Error("unsupported nonzero numeric env silently accepted")
		}
		_ = h.env.SetProperty(quote("CLAUDE_CODE_DISABLE_WORKFLOWS"), nil)
		_ = h.env.SetProperty(quote("USER_TYPE"), quote("ant"))
		future, e := h.service.BeginFreshCommands(scope, nil)
		if e != nil {
			return e
		}
		if _, e = future.Await(scope); e == nil {
			t.Error("changed host controls escaped async source")
		}
		return nil
	})
}
func TestNativeProjectWalkFailureAndHomeBoundary(t *testing.T) {
	h := newHost(t)
	if e := os.WriteFile(filepath.Join(h.cwd, ".claude"), []byte("native non-directory ancestor"), 0600); e != nil {
		t.Fatal(e)
	}
	h.run(t, func(scope *cooperative.Scope) error {
		future, e := h.service.BeginDefinitions(scope, nil)
		if e != nil {
			return e
		}
		values, e := future.Await(scope)
		if e != nil {
			return e
		}
		if len(values) != 1 || len(h.diagnostics) != 1 {
			t.Error(values, h.diagnostics)
		}
		if e = h.service.ResetBundledForTesting(scope); e != nil {
			return e
		}
		if e = h.state.SetOriginalCWD(scope, h.home); e != nil {
			return e
		}
		if e = h.state.SetProjectRoot(scope, h.home); e != nil {
			return e
		}
		future, e = h.service.BeginDefinitions(scope, nil)
		if e != nil {
			return e
		}
		values, e = future.Await(scope)
		if e != nil {
			return e
		}
		if len(values) != 1 {
			t.Error(values)
		}
		return nil
	})
}
func TestActualFactorySourceMappingAndReferenceConsumption(t *testing.T) {
	h := newHost(t)
	h.run(t, func(scope *cooperative.Scope) error {
		for _, source := range []string{"built-in", "projectSettings", "plugin", "userSettings", "future"} {
			raw := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote("original")}, protocol.JSONField{Name: "description", Value: quote("中文🚀")}, protocol.JSONField{Name: "source", Value: quote(source)})
			command, e := NewCommand(scope, h.service.options.Gate, Definition{raw})
			if e != nil {
				return e
			}
			raw.Set("name", quote("changed"))
			raw.Set("description", quote("later"))
			descriptor, e := command.Descriptor(scope)
			if e != nil {
				return e
			}
			if string(descriptor.Get("name")) != `"original"` || string(descriptor.Get("description")) != string(quote("中文🚀")) || string(descriptor.Get("contentLength")) != "204" {
				t.Error(descriptor)
			}
			if live, e := command.UserFacingName(scope); e != nil || live != "changed" {
				t.Error(live, e)
			}
			f, e := command.BeginPrompt(scope, quote("arg"))
			if e != nil {
				return e
			}
			output, e := f.Await(scope)
			if e != nil {
				return e
			}
			if !strings.Contains(string(output), `changed`) {
				t.Error(string(output))
			}
			raw.Set("name", quote("🚀"))
			if _, e = command.BeginPrompt(scope, quote("arg")); e == nil {
				t.Error("unsupported live non-ASCII name escaped")
			}
		}
		return nil
	})
	var nilCommand *Command
	if _, e := nilCommand.BeginPrompt(nil, quote("arg")); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	var nilService *Service
	if e := nilService.ClearCommandCache(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	var nilGate *Gate
	if _, e := nilGate.DisabledReason(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
}

func TestUnsupportedActiveSettingsRejectsCommandsBeforeDiscovery(t *testing.T) {
	h := newHost(t)
	if e := os.WriteFile(filepath.Join(h.config, "settings.json"), []byte(`{"apiKeyHelper":"unmigrated-helper"}`), 0600); e != nil {
		t.Fatal(e)
	}
	h.run(t, func(scope *cooperative.Scope) error {
		f, e := h.service.BeginFreshCommands(scope, nil)
		if e != nil {
			return e
		}
		if _, e = f.Await(scope); e == nil {
			t.Error("active unsupported settings escaped gate")
		}
		return nil
	})
}
func TestOriginalConfigHomeFailureRejectsDefinitionFuture(t *testing.T) {
	h := newHost(t)
	cause := errors.New("original homedir capability unavailable")
	resolver, e := bootstrapstate.NewConfigResolver(h.owner, bootstrapstate.ConfigHost{Environment: func(string) (string, bool) { return "", false }, HomeDir: func() (string, error) { return "", cause }})
	if e != nil {
		t.Fatal(e)
	}
	h.service.options.Config = resolver
	h.run(t, func(scope *cooperative.Scope) error {
		f, e := h.service.BeginDefinitions(scope, nil)
		if e != nil {
			return e
		}
		if _, e = f.Await(scope); !errors.Is(e, cause) {
			t.Error("config cause replaced", e)
		}
		return nil
	})
}
func TestCancelledPromptPreservesAdministrativeCause(t *testing.T) {
	h := newHost(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original caller scope cancelled")
	e := h.owner.Run(ctx, func(scope *cooperative.Scope) error {
		raw := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote("original")}, protocol.JSONField{Name: "description", Value: quote("description")})
		command, e := NewCommand(scope, h.service.options.Gate, Definition{raw})
		if e != nil {
			return e
		}
		cancel(cause)
		if _, e = command.BeginPrompt(scope, quote("original input")); !errors.Is(e, cause) {
			t.Error("cancelled scope published prompt", e)
		}
		return nil
	})
	if e != nil && !errors.Is(e, cause) {
		t.Error(e)
	}
}
func TestNativeReadDirectoryOfFileReportsActualError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if e := os.WriteFile(path, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := (OSFileSystem{}).ReadDir(path); e == nil {
		t.Fatal("native readdir(file) accepted")
	}
}
