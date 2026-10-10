package skillregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type fixturePlugins struct {
	plugins []Plugin
	err     error
}

func (p fixturePlugins) EnabledForSkills(context.Context) ([]Plugin, error) { return p.plugins, p.err }
func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func testService(t *testing.T, home, cwd string, plugins []Plugin, settings protocol.JSONObject, env string) *Service {
	t.Helper()
	service, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), CWD: cwd, ProjectRoot: cwd, Environment: func(string) string { return env }, Settings: func(context.Context) (protocol.JSONObject, error) { return settings, nil }, Plugins: fixturePlugins{plugins: plugins}})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func TestActualFilesRootPrecedenceAndMissingNativeDetail(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo", "pkg")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(home, "repo", ".git", "HEAD"), "ref: refs/heads/main\n")
	put(t, filepath.Join(home, ".claude", "skills", "shared", "SKILL.md"), "---\ndescription: Native\n---\nbody")
	put(t, filepath.Join(home, ".agents", "skills", "shared", "SKILL.md"), "---\ndescription: Agent fallback\n---\nbody")
	put(t, filepath.Join(cwd, ".agents", "skills", "near", "SKILL.md"), "Closest")
	put(t, filepath.Join(home, "repo", ".claude", "skills", "near", "SKILL.md"), "Parent native")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "fallback"), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(home, ".agents", "skills", "fallback", "SKILL.md"), "Fallback")
	service := testService(t, home, cwd, nil, protocol.NewJSONObject(), "")
	list, err := service.List(context.Background(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Name != "fallback" || list[1].Name != "near" || list[1].RootFlavor != "agents" || list[2].Name != "shared" || string(list[2].Description) != `"Native"` {
		t.Fatalf("precedence differs: %+v", list)
	}
	_, err = service.Detail(context.Background(), cwd, "user", "fallback")
	var api *Error
	if !errors.As(err, &api) || api.Status != 404 || api.Message != "Skill missing SKILL.md: fallback" {
		t.Fatalf("detail must select first existing directory: %v", err)
	}
	if !Outranks("agents", "claude") || Outranks("claude", "agents") || Outranks("", "claude") {
		t.Fatal("flavor override differs")
	}
}
func TestTreeFileLimitsAndRawMetadata(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".claude", "skills", "item")
	put(t, filepath.Join(root, "SKILL.md"), "---\ndescription: true\nname: [one, two]\nuser-invocable: 'false'\nversion: .inf\n---\nbody 😀\n")
	put(t, filepath.Join(root, ".hidden.md"), "hidden")
	put(t, filepath.Join(root, "node_modules", "ignored.js"), "ignored")
	put(t, filepath.Join(root, "00-large.txt"), strings.Repeat("x", 100*1024+1))
	for i := 0; i < 52; i++ {
		put(t, filepath.Join(root, "script", fmt.Sprintf("%02d.go", i)), "package fixture")
	}
	service := testService(t, home, cwd, nil, protocol.NewJSONObject(), "")
	detail, err := service.Detail(context.Background(), cwd, "user", "item")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Files) != 50 || len(detail.Tree) != 1 || len(detail.Tree[0].Children) != 50 || detail.Files[0].Language != "go" {
		t.Fatalf("directory-first success budget differs: %+v", detail)
	}
	if string(detail.Meta.Description) != "true" || string(detail.Meta.DisplayName) != `["one","two"]` || !detail.Meta.UserInvocable || detail.Meta.Version == nil || *detail.Meta.Version != "Infinity" {
		t.Fatalf("metadata coercion differs: %+v", detail.Meta)
	}
	if err := os.RemoveAll(filepath.Join(root, "script")); err != nil {
		t.Fatal(err)
	}
	detail, err = service.Detail(context.Background(), cwd, "user", "item")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Tree) != 2 || detail.Tree[0].Name != "00-large.txt" || len(detail.Files) != 1 || string(detail.Files[0].Content) != `"body 😀\n"` || detail.Files[0].Frontmatter == nil || !detail.Files[0].IsEntry {
		t.Fatalf("oversized tree/entry differs: %+v", detail)
	}
}
func TestPluginDirectAndHiddenDirectoriesKeepExactIdentity(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "plugin", "skills")
	put(t, filepath.Join(root, ".hidden", "SKILL.md"), "Hidden plugin Skill")
	direct := filepath.Join(home, "direct", "single")
	put(t, filepath.Join(direct, "SKILL.md"), "Direct Skill")
	put(t, filepath.Join(direct, "nested", "SKILL.md"), "Must not scan")
	service := testService(t, home, cwd, []Plugin{{Name: "sample", SkillsPath: root, SkillsPaths: []string{direct, root}}}, protocol.NewJSONObject(), "")
	list, err := service.List(context.Background(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "sample:.hidden" || list[1].Name != "sample:single" || list[0].RootFlavor != "" || list[1].PluginName != "sample" {
		t.Fatalf("plugin identity differs: %+v", list)
	}
}
func TestDetailsRejectTraversalAndReadFreshChanges(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "skills", "item", "SKILL.md")
	put(t, path, "first")
	service := testService(t, home, cwd, nil, protocol.NewJSONObject(), "")
	for _, name := range []string{"../item", "a/b", `a\b`, "x..y"} {
		_, err := service.Detail(context.Background(), cwd, "user", name)
		var api *Error
		if !errors.As(err, &api) || api.Status != 400 {
			t.Fatalf("unsafe name: %q %v", name, err)
		}
	}
	first, err := service.Detail(context.Background(), cwd, "user", "item")
	if err != nil {
		t.Fatal(err)
	}
	put(t, path, "second")
	second, err := service.Detail(context.Background(), cwd, "user", "item")
	if err != nil || string(first.Files[0].Content) != `"first"` || string(second.Files[0].Content) != `"second"` {
		t.Fatalf("stale detail: %v %+v", err, second)
	}
}
func TestAgentRootDisableAndSettingsFailure(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(cwd, ".agents", "skills", "item", "SKILL.md"), "Agent Skill")
	service := testService(t, home, cwd, nil, protocol.NewJSONObject(protocol.JSONField{Name: "disableAgentSkillsDirectory", Value: json.RawMessage("true")}), "")
	list, err := service.List(context.Background(), cwd)
	if err != nil || len(list) != 0 || len(service.UserRoots(context.Background())) != 1 {
		t.Fatalf("settings disable differs: %+v %v", list, err)
	}
	service.options.Settings = func(context.Context) (protocol.JSONObject, error) {
		return protocol.JSONObject{}, errors.New("invalid settings")
	}
	if len(service.UserRoots(context.Background())) != 2 {
		t.Fatal("settings failure must default enabled")
	}
	service.options.Environment = func(string) string { return "\ufeff YeS \ufeff" }
	if len(service.UserRoots(context.Background())) != 1 {
		t.Fatal("env disable differs")
	}
}
func TestMarketMetadataStrictShape(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".claude", "skills", "item")
	put(t, filepath.Join(root, "SKILL.md"), "Skill")
	meta := filepath.Join(root, ".market-meta.json")
	put(t, meta, `{"id":"clawhub:Item","source":"clawhub","slug":"Item","installedAt":"timestamp","fileCount":1,"future":"ignore","version":""}`)
	service := testService(t, home, cwd, nil, protocol.NewJSONObject(), "")
	detail, err := service.Detail(context.Background(), cwd, "user", "item")
	if err != nil || detail.MarketMeta == nil || len(detail.MarketMeta.Get("future")) != 0 {
		t.Fatalf("market meta: %+v %v", detail, err)
	}
	put(t, meta, `{"id":"clawhub:item","source":"clawhub","slug":"item","installedAt":"timestamp","fileCount":null}`)
	detail, err = service.Detail(context.Background(), cwd, "user", "item")
	if err != nil || detail.MarketMeta != nil {
		t.Fatalf("null count accepted: %+v %v", detail, err)
	}
}
