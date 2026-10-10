package agentcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type realFiles struct {
	root         string
	faults       map[string]error
	noIdentity   bool
	readFailures map[string]error
	statCalls    []string
}

func (f *realFiles) admissible(p string) error {
	if !filepath.IsAbs(p) {
		return cap("fixture absolute path")
	}
	rel, e := filepath.Rel(f.root, p)
	if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return fs.ErrNotExist
	}
	return nil
}
func (f *realFiles) Stat(p string) (fs.FileInfo, error) {
	f.statCalls = append(f.statCalls, p)
	if e := f.admissible(p); e != nil {
		return nil, e
	}
	if e := f.faults[p]; e != nil {
		return nil, e
	}
	return os.Stat(p)
}
func (f *realFiles) Lstat(p string) (fs.FileInfo, error) {
	if e := f.admissible(p); e != nil {
		return nil, e
	}
	if e := f.faults["lstat:"+p]; e != nil {
		return nil, e
	}
	return os.Lstat(p)
}
func (f *realFiles) ReadFile(p string) ([]byte, error) {
	if e := f.admissible(p); e != nil {
		return nil, e
	}
	if e := f.readFailures[p]; e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}
func (f *realFiles) ReadDir(p string) ([]fs.DirEntry, error) {
	if e := f.admissible(p); e != nil {
		return nil, e
	}
	if e := f.faults["readdir:"+p]; e != nil {
		return nil, e
	}
	return os.ReadDir(p)
}
func (f *realFiles) Realpath(p string) (string, error) {
	if e := f.admissible(p); e != nil {
		return "", e
	}
	if e := f.faults["realpath:"+p]; e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(p)
}
func (f *realFiles) Identity(i fs.FileInfo) string {
	if f.noIdentity {
		return ""
	}
	return fixtureIdentity(i)
}

type fixture struct {
	t                                *testing.T
	root, home, cwd, config, managed string
	owner                            *cooperative.Owner
	fs                               *realFiles
	env                              map[string]string
	settings                         map[string]string
	now                              func() time.Time
	plugins                          func(*cooperative.Scope) ([]Definition, error)
	sources                          []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	r := t.TempDir()
	f := &fixture{t: t, root: r, home: filepath.Join(r, "home"), cwd: filepath.Join(r, "workspace"), managed: filepath.Join(r, "managed"), env: map[string]string{"USER_TYPE": "external", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH": "1", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli"}, settings: map[string]string{}, owner: cooperative.New(), sources: []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"}, now: time.Now}
	f.config = filepath.Join(f.home, ".claude")
	f.fs = &realFiles{root: r, faults: map[string]error{}, readFailures: map[string]error{}}
	for _, p := range []string{f.home, f.cwd, f.config, f.managed, filepath.Join(f.cwd, ".git")} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	t.Cleanup(f.owner.Close)
	f.plugins = func(*cooperative.Scope) ([]Definition, error) { return nil, nil }
	return f
}
func (f *fixture) write(path, content string) {
	f.t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		f.t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(content), 0600); e != nil {
		f.t.Fatal(e)
	}
}
func (f *fixture) settingsGet(source string) (*protocol.JSONObject, error) {
	p := f.settings[source]
	if p == "" {
		return nil, nil
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	parsed, e := clisettings.ValidateJSONContent(string(raw), p)
	if e != nil {
		return nil, e
	}
	if len(parsed.Errors) > 0 {
		return nil, fmt.Errorf("schema: %v", parsed.Errors)
	}
	return parsed.Settings, nil
}
func (f *fixture) service() *Service {
	f.t.Helper()
	s, e := New(Options{Owner: f.owner, Files: f.fs, Home: f.home, CWD: f.cwd, ProjectRoot: f.cwd, ConfigHome: f.config, ManagedRoot: f.managed, EnabledSources: f.sources, Environment: func(k string) string { return f.env[k] }, Settings: f.settingsGet, Now: f.now, NonInteractive: true, Plugins: f.plugins})
	if e != nil {
		f.t.Fatal(e)
	}
	return s
}
func (f *fixture) list(s *Service) (*Result, error) {
	var r *Result
	e := f.owner.Run(context.Background(), func(scope *cooperative.Scope) error { var e error; r, e = s.List(scope); return e })
	return r, e
}
func markdown(name, model, memory string) string {
	return "---\nname: " + name + "\ndescription: private business fixture\nmodel: " + model + "\nmemory: " + memory + "\n---\nbody"
}
func findName(all []Definition, name string) *Definition {
	for i := range all {
		if all[i].Name == name {
			return &all[i]
		}
	}
	return nil
}
func TestActualDiscoveryClosestManagedOverrideAndUnknownFields(t *testing.T) {
	f := newFixture(t)
	originalRoot := f.cwd
	f.cwd = filepath.Join(f.cwd, "nested")
	if e := os.MkdirAll(f.cwd, 0700); e != nil {
		t.Fatal(e)
	}
	old, e := os.ReadFile("testdata/old-reader.md")
	if e != nil {
		t.Fatal(e)
	}
	f.write(filepath.Join(f.config, "agents/a.md"), string(old))
	f.write(filepath.Join(originalRoot, ".claude/agents/a.md"), markdown("Shared", "root-model", "local"))
	f.write(filepath.Join(f.cwd, ".claude/agents/z.md"), markdown("Shared", "near-model", "user"))
	f.write(filepath.Join(f.managed, ".claude/agents/managed.md"), markdown("Shared", "managed-model", "project"))
	f.write(filepath.Join(f.cwd, ".claude/agents/.hidden.md"), markdown("Hidden", "inherit", "local"))
	f.write(filepath.Join(f.cwd, ".claude/agents/uppercase.MD"), markdown("Ignored", "inherit", "local"))
	f.write(filepath.Join(f.cwd, ".claude/agents/reference.md"), "reference notes without frontmatter")
	f.write(filepath.Join(f.cwd, ".claude/agents/invalid.md"), "---\nname: Invalid\ndescription: test\nmodel: null\n---\nx")
	r, e := f.list(f.service())
	if e != nil {
		t.Fatal(e)
	}
	winner := findName(r.Active, "Shared")
	if winner == nil || winner.Model != "managed-model" || winner.Source != "policySettings" {
		t.Fatalf("winner=%+v", winner)
	}
	if findName(r.Active, "Ignored") != nil || len(r.FailedFiles) != 1 || !strings.Contains(r.FailedFiles[0].Error, `Invalid "model"`) {
		t.Fatalf("result=%+v", r)
	}
	user := []Definition{}
	for _, d := range r.All {
		if d.Source == "userSettings" {
			user = append(user, d)
		}
	}
	if len(user) != 1 || user[0].Frontmatter.Get("future") == nil || user[0].Description != "read\nprivate documents" || string(user[0].Effort) != "-12" || len(user[0].ContentHash) != 64 || user[0].Body != "A private read-only prompt." {
		t.Fatalf("old input=%+v", user)
	}
	rendered := Render(r.All)
	if !strings.Contains(rendered, "(shadowed by managed) Shared · near-model · user memory") || strings.Contains(rendered, "root-model") {
		t.Fatalf("render=%s", rendered)
	}
	// Cache uses the real first read. Explicit clear permits the changed file to be observed.
	s := f.service()
	r, e = f.list(s)
	if e != nil {
		t.Fatal(e)
	}
	f.write(filepath.Join(f.cwd, ".claude/agents/new.md"), markdown("New", "inherit", ""))
	same, e := f.list(s)
	if e != nil || same != r || findName(same.Active, "New") != nil {
		t.Fatal("cache changed without clear")
	}
	if e = f.owner.Run(context.Background(), func(sc *cooperative.Scope) error { return s.Clear(sc) }); e != nil {
		t.Fatal(e)
	}
	fresh, e := f.list(s)
	if e != nil || findName(fresh.Active, "New") == nil {
		t.Fatalf("clear=%v %+v", e, fresh)
	}
}
func TestSharedSettingsOverridesAndManagedRestrictionAreConsumed(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.config, "agents/a.md"), markdown("UserOnly", "inherit", ""))
	f.write(filepath.Join(f.cwd, ".claude/agents/a.md"), markdown("ProjectOnly", "inherit", ""))
	f.write(filepath.Join(f.managed, ".claude/agents/a.md"), markdown("ManagedOnly", "inherit", ""))
	f.settings["userSettings"] = filepath.Join(f.config, "settings.json")
	f.settings["projectSettings"] = filepath.Join(f.cwd, ".claude/settings.json")
	f.settings["policySettings"] = filepath.Join(f.managed, "policy.json")
	f.write(f.settings["policySettings"], `{}`)
	f.write(f.settings["userSettings"], `{"builtInAgentOverrides":{"Explore":{"model":" user-model ","effort":-13,"future":true}}}`)
	f.write(f.settings["projectSettings"], `{"builtInAgentOverrides":{"Explore":{"model":" project-model ","effort":"invalid"}}}`)
	r, e := f.list(f.service())
	if e != nil {
		t.Fatal(e)
	}
	explore := findName(r.All, "Explore")
	if explore.Model != "project-model" || explore.ModelSource != "projectSettings" || string(explore.Effort) != "-13" || explore.EffortSource != "userSettings" {
		t.Fatalf("fieldwise=%+v", explore)
	}
	f.write(f.settings["policySettings"], `{"strictPluginOnlyCustomization":["future","agents"],"builtInAgentOverrides":{"Explore":{"model":" policy-model ","effort":"high"}}}`)
	r, e = f.list(f.service())
	if e != nil {
		t.Fatal(e)
	}
	if findName(r.All, "UserOnly") != nil || findName(r.All, "ProjectOnly") != nil || findName(r.All, "ManagedOnly") == nil || findName(r.All, "Explore").Model != "policy-model" {
		t.Fatalf("restricted=%+v", r.All)
	}
	// A user-owned restriction has no authority; only the policy source is trusted.
	f.write(f.settings["policySettings"], `{"strictPluginOnlyCustomization":false}`)
	f.write(f.settings["userSettings"], `{"strictPluginOnlyCustomization":true}`)
	r, e = f.list(f.service())
	if e != nil || findName(r.All, "UserOnly") == nil {
		t.Fatalf("untrusted policy=%v %+v", e, r)
	}
	f.sources = []string{"flagSettings", "policySettings"}
	r, e = f.list(f.service())
	if e != nil || findName(r.All, "UserOnly") != nil || findName(r.All, "ManagedOnly") == nil {
		t.Fatalf("explicit empty sources=%v %+v", e, r)
	}
}
func TestNativeWalkFollowsLinksCyclesAndPhysicalDedup(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.config, "agents/a.md")
	f.write(p, markdown("Linked", "inherit", ""))
	dir := filepath.Join(f.cwd, ".claude/agents")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(p, filepath.Join(dir, "hard.md")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(p, filepath.Join(dir, "sym.md")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(dir, filepath.Join(dir, "cycle")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("missing", filepath.Join(dir, "dangling.md")); e != nil {
		t.Fatal(e)
	}
	r, e := f.list(f.service())
	if e != nil {
		t.Fatal(e)
	}
	linked := []Definition{}
	for _, d := range r.All {
		if d.Name == "Linked" {
			linked = append(linked, d)
		}
	}
	if len(linked) != 2 || linked[0].Source != "userSettings" || linked[1].FilePath != filepath.Join(dir, "sym.md") {
		t.Fatalf("dedup lstat (symlink owns different inode)=%+v", linked)
	}
	f.fs.noIdentity = true
	r, e = f.list(f.service())
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, d := range r.All {
		if d.Name == "Linked" {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("identity fail-open count=%d", count)
	}
	f.fs.faults["realpath:"+dir] = errors.New("canonical failure")
	r, e = f.list(f.service())
	if e != nil || findName(r.All, "Linked") == nil {
		t.Fatalf("fallback=%v %+v", e, r)
	}
}
func TestWorktreeFallbackAndNestedRepositoryBoundary(t *testing.T) {
	f := newFixture(t)
	main := filepath.Join(f.root, "main")
	work := f.cwd
	gitdir := filepath.Join(main, ".git/worktrees/w")
	if e := os.RemoveAll(filepath.Join(work, ".git")); e != nil {
		t.Fatal(e)
	}
	f.write(filepath.Join(work, ".git"), "gitdir: "+gitdir+"\n")
	f.write(filepath.Join(gitdir, "commondir"), "../..\n")
	f.write(filepath.Join(gitdir, "gitdir"), filepath.Join(work, ".git")+"\n")
	f.write(filepath.Join(main, ".claude/agents/main.md"), markdown("Fallback", "inherit", ""))
	s := f.service()
	if got := s.canonical(work); got != main {
		t.Fatalf("canonical=%s", got)
	}
	r, e := f.list(s)
	if e != nil || findName(r.All, "Fallback") == nil {
		t.Fatalf("fallback=%v %+v", e, r)
	}
	f.write(filepath.Join(work, ".claude/agents/own.md"), markdown("Own", "inherit", ""))
	r, e = f.list(f.service())
	if e != nil || findName(r.All, "Fallback") != nil || findName(r.All, "Own") == nil {
		t.Fatalf("own copy=%v %+v", e, r)
	}
	f.write(filepath.Join(gitdir, "gitdir"), filepath.Join(main, "wrong.git"))
	if got := s.canonical(work); got != work {
		t.Fatalf("invalid backlink=%s", got)
	}
	// Independent nested repository widens discovery to the original session root.
	nested := filepath.Join(work, "nested")
	if e = os.MkdirAll(filepath.Join(nested, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	f.cwd = nested
	s = f.service()
	s.options.ProjectRoot = work
	dirs, e := s.projectDirs()
	if e != nil || !reflect.DeepEqual(dirs, []string{filepath.Join(work, ".claude/agents")}) {
		t.Fatalf("nested=%v %v", dirs, e)
	}
}
func TestGenericDiscoveryCatchCapabilitiesAndReadErrors(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.cwd, ".claude/agents/a.md")
	f.write(p, markdown("Read", "inherit", ""))
	f.fs.faults[filepath.Dir(p)] = errors.New("guard generic failure")
	r, e := f.list(f.service())
	if e != nil || len(r.All) != 5 || len(r.FailedFiles) != 1 || r.FailedFiles[0].Path != "unknown" {
		t.Fatalf("outer catch=%v %+v", e, r)
	}
	f.fs.faults[filepath.Dir(p)] = cap("real FS unsupported")
	if _, e = f.list(f.service()); e == nil {
		t.Fatal("capability swallowed")
	}
	delete(f.fs.faults, filepath.Dir(p))
	f.fs.readFailures[p] = fs.ErrPermission
	r, e = f.list(f.service())
	if e != nil || findName(r.All, "Read") != nil {
		t.Fatalf("read skip=%v %+v", e, r)
	}
	f.fs.readFailures[p] = cap("read provenance")
	if _, e = f.list(f.service()); e == nil {
		t.Fatal("read capability swallowed")
	}
	delete(f.fs.readFailures, p)
	f.fs.faults["readdir:"+filepath.Dir(p)] = errors.New("directory unreadable")
	r, e = f.list(f.service())
	if e != nil || findName(r.All, "Read") != nil {
		t.Fatal("native generic directory failure must skip")
	}
	f.fs.faults["readdir:"+filepath.Dir(p)] = cap("readdir provenance")
	if _, e = f.list(f.service()); e == nil {
		t.Fatal("directory capability swallowed")
	}
	delete(f.fs.faults, "readdir:"+filepath.Dir(p))
	f.env["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "0"
	if _, e = f.list(f.service()); e == nil || !strings.Contains(e.Error(), "ripgrep") {
		t.Fatalf("default=%v", e)
	}
	f.env["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	f.plugins = nil
	if _, e = f.list(f.service()); e == nil {
		t.Fatal("plugin absence fabricated")
	}
	f.plugins = func(*cooperative.Scope) ([]Definition, error) { return nil, errors.New("plugin failure") }
	r, e = f.list(f.service())
	if e != nil || len(r.FailedFiles) != 1 {
		t.Fatalf("plugin catch=%v %+v", e, r)
	}
	f.plugins = func(*cooperative.Scope) ([]Definition, error) { return nil, cap("active plugin") }
	if _, e = f.list(f.service()); e == nil {
		t.Fatal("plugin capability swallowed")
	}
	f.plugins = func(*cooperative.Scope) ([]Definition, error) { return nil, nil }
	f.now = func() time.Time { return time.Unix(0, 0) }
	s := f.service()
	calls := 0
	s.options.Now = func() time.Time { calls++; return time.Unix(0, int64(calls)*4e9) }
	r, e = f.list(s)
	if e != nil || findName(r.All, "Read") != nil {
		t.Fatal("native discovery deadline must return partial, not block")
	}
}
func TestBuiltinSDKInteractiveSimpleAndLifecycle(t *testing.T) {
	f := newFixture(t)
	s := f.service()
	r, e := f.list(s)
	if e != nil || len(r.All) != 5 || findName(r.All, "claude-code-guide") != nil {
		t.Fatalf("SDK=%v %+v", e, r)
	}
	s = f.service()
	s.options.NonInteractive = false
	f.env["CLAUDE_CODE_ENTRYPOINT"] = "cli"
	r, e = f.list(s)
	if e != nil || len(r.All) != 6 || findName(r.All, "claude-code-guide") == nil {
		t.Fatal("interactive guide missing")
	}
	f.env["CLAUDE_AGENT_SDK_DISABLE_BUILTIN_AGENTS"] = "yes"
	r, e = f.list(f.service())
	if e != nil || len(r.All) != 0 || Render(r.All) != "No agents found.\n" {
		t.Fatalf("disabled=%v %+v", e, r)
	}
	s = f.service()
	s.options.NonInteractive = false
	r, e = f.list(s)
	if e != nil || len(r.All) != 6 {
		t.Fatal("interactive must ignore SDK disable")
	}
	f.env["CLAUDE_CODE_SIMPLE"] = "1"
	f.env["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "0"
	f.plugins = nil
	s = f.service()
	r, e = f.list(s)
	if e != nil || len(r.All) != 0 {
		t.Fatalf("simple no discovery=%v %+v", e, r)
	}
	f.env["USER_TYPE"] = "ant"
	if _, e = f.list(f.service()); e == nil {
		t.Fatal("internal builtin producer fabricated")
	}
}
func TestSourceGroupsActiveAndExactOutput(t *testing.T) {
	all := []Definition{{Name: "X", Source: "built-in"}, {Name: "X", Source: "plugin", Model: "plugin"}, {Name: "X", Source: "userSettings", Model: "user"}, {Name: "X", Source: "projectSettings", Model: "near"}, {Name: "X", Source: "projectSettings", Model: "far"}, {Name: "X", Source: "flagSettings", Model: "flag"}, {Name: "X", Source: "policySettings", Model: "managed"}, {Name: "OnlyLocal", Source: "localSettings", Memory: "local"}, {Name: "alpha", Source: "userSettings"}, {Name: "Bravo", Source: "userSettings"}}
	active := Active(all)
	if active[0].Source != "policySettings" || findName(active, "OnlyLocal") != nil {
		t.Fatalf("active=%+v", active)
	}
	output := Render(all)
	want := "4 active agents\n\nUser agents:\n  alpha · inherit\n  Bravo · inherit\n  (shadowed by managed) X · user\n\nProject agents:\n  (shadowed by managed) X · near\n\nLocal agents:\n  OnlyLocal · inherit · local memory\n\nManaged agents:\n  X · managed\n\nPlugin agents:\n  (shadowed by managed) X · plugin\n\nCLI arg agents:\n  (shadowed by managed) X · flag\n\nBuilt-in agents:\n  (shadowed by managed) X · inherit\n"
	if output != want {
		t.Fatalf("stdout got=%q want=%q", output, want)
	}
	if len(Groups()) != 7 || sourceLabel("future") != "future" || sourceLabel("built-in") != "built-in" || sourceLabel("localSettings") != "local" {
		t.Fatal("source labels")
	}
}
func TestInvalidBindingsOwnerAndCachedCancellation(t *testing.T) {
	if _, e := New(Options{}); e == nil {
		t.Fatal("missing bindings admitted")
	}
	f := newFixture(t)
	s := f.service()
	bad := s.options
	bad.Home = "relative"
	if _, e := New(bad); e == nil {
		t.Fatal("relative path")
	}
	bad = s.options
	bad.EnabledSources = []string{"future"}
	if _, e := New(bad); e == nil {
		t.Fatal("unknown sources")
	}
	other := cooperative.New()
	defer other.Close()
	if e := other.Run(context.Background(), func(sc *cooperative.Scope) error { _, e := s.List(sc); return e }); e == nil {
		t.Fatal("foreign owner")
	}
	if e := other.Run(context.Background(), s.Clear); e == nil {
		t.Fatal("foreign clear")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := f.owner.Run(ctx, func(sc *cooperative.Scope) error { _, e := s.List(sc); return e }); e == nil {
		t.Fatal("cancelled owner admission")
	}
	f.owner.Close()
	if _, e := f.list(s); e == nil {
		t.Fatal("closed owner")
	}
}

var _ = json.Valid

func TestNoRepositoryStopsAtHomeAndDiscoveryStillRunsForDisabledProject(t *testing.T) {
	f := newFixture(t)
	if e := os.RemoveAll(filepath.Join(f.cwd, ".git")); e != nil {
		t.Fatal(e)
	}
	f.home = f.root
	f.write(filepath.Join(f.cwd, ".claude/agents/own.md"), markdown("Only", "inherit", ""))
	f.write(filepath.Join(f.home, ".claude/agents/out.md"), markdown("MustNotLoad", "inherit", ""))
	r, e := f.list(f.service())
	if e != nil || findName(r.All, "Only") == nil || findName(r.All, "MustNotLoad") != nil {
		t.Fatalf("home stop=%v %+v", e, r)
	}
	f.sources = []string{"flagSettings", "policySettings"}
	f.fs.faults[filepath.Join(f.cwd, ".claude/agents")] = errors.New("stat generic still happens")
	r, e = f.list(f.service())
	if e != nil || len(r.FailedFiles) != 1 || r.FailedFiles[0].Error != "stat generic still happens" {
		t.Fatalf("disabled source discovery=%v %+v", e, r)
	}
}
func TestWorktreeBadMetadataFallsBackWithoutReadingAnotherRepository(t *testing.T) {
	f := newFixture(t)
	if e := os.RemoveAll(filepath.Join(f.cwd, ".git")); e != nil {
		t.Fatal(e)
	}
	gitdir := filepath.Join(f.root, "git/worktrees/w")
	f.write(filepath.Join(f.cwd, ".git"), "ordinary non-pointer data")
	s := f.service()
	if s.canonical(f.cwd) != f.cwd {
		t.Fatal("invalid git file changed root")
	}
	f.write(filepath.Join(f.cwd, ".git"), "gitdir: "+gitdir)
	if s.canonical(f.cwd) != f.cwd {
		t.Fatal("missing commondir changed root")
	}
	f.write(filepath.Join(gitdir, "commondir"), "unrelated-common")
	if s.canonical(f.cwd) != f.cwd {
		t.Fatal("non-worktree metadata changed root")
	}
	f.write(filepath.Join(gitdir, "commondir"), "../..")
	if s.canonical(f.cwd) != f.cwd {
		t.Fatal("missing backlink changed root")
	}
	f.write(filepath.Join(gitdir, "gitdir"), filepath.Join(f.cwd, ".git"))
	if s.canonical(f.cwd) != filepath.Join(f.root, "git") {
		t.Fatal("bare common directory root")
	}
	f.fs.faults["realpath:"+f.cwd] = errors.New("current root unavailable")
	if s.canonical(f.cwd) != f.cwd {
		t.Fatal("unavailable canonical root changed boundary")
	}
}
func TestAgentSettingsErrorsDoNotBecomeAnEmptyPolicy(t *testing.T) {
	f := newFixture(t)
	s := f.service()
	s.options.Settings = func(string) (*protocol.JSONObject, error) { return nil, errors.New("real settings error") }
	if _, e := f.list(s); e == nil {
		t.Fatal("settings failure hidden")
	}
	f.env["CLAUDE_CODE_SIMPLE"] = "1"
	if _, e := f.list(s); e == nil {
		t.Fatal("bare settings failure hidden")
	}
	f.env["CLAUDE_CODE_SIMPLE"] = ""
	f.plugins = func(*cooperative.Scope) ([]Definition, error) { return nil, errors.New("plugin failed") }
	s = f.service()
	s.options.Settings = func(source string) (*protocol.JSONObject, error) {
		if source == "userSettings" {
			return nil, errors.New("builtin source failed")
		}
		return nil, nil
	}
	if _, e := f.list(s); e == nil {
		t.Fatal("builtin catch failure hidden")
	}
	f = newFixture(t)
	s = f.service()
	settings := protocol.NewJSONObject()
	_ = settings.UnmarshalJSON([]byte(`{"builtInAgentOverrides":{"Explore":{"model":42},"Plan":false,"verification":{"model":"\ud800"}}}`))
	s.options.Settings = func(source string) (*protocol.JSONObject, error) {
		if source == "userSettings" {
			return &settings, nil
		}
		return nil, nil
	}
	if _, e := f.list(s); e == nil || !strings.Contains(e.Error(), "UTF16") {
		t.Fatalf("unported UTF16 model was replaced=%v", e)
	}
}
