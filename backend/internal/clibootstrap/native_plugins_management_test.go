package clibootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestNativePluginManagementSharesDefinitionsSettingsAndHookRegistry(t *testing.T) {
	f := newNativePluginFixture(t)
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.write(t, filepath.Join(f.plugin, "agents", "runner.md"), "---\nname: runner\ndescription: Fixture runner\n---\nRead the task.")
	f.write(t, filepath.Join(f.plugin, "hooks", "hooks.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"fixture-only"}]}]}}`)
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/private-fixture\n")
	store := storage.New(storage.Options{})
	p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	upgrader, err := migrations.New(migrations.Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	editable, err := settings.New(settings.Options{Store: store, GitExecutable: "git", EnsureMigrated: upgrader.SettingsCallback, HomeDir: f.home, ConfigDir: f.config, ProjectRoot: f.cwd, Environment: func() map[string]string { return f.n.Environment }, ShellEnvironment: func(context.Context) (map[string]string, error) { return f.n.Environment, nil }, InvalidateCache: p.InvalidateSettingsCache})
	if err != nil {
		t.Fatal(err)
	}
	defer editable.DrainBackground()
	secure, err := securestorage.New(securestorage.Options{Home: f.home, ConfigDir: f.config, Platform: "linux", Environment: f.n.Environment})
	if err != nil {
		t.Fatal(err)
	}
	options, err := p.PluginOptions(secure)
	if err != nil {
		t.Fatal(err)
	}
	mcp, _, err := p.PluginMCP(options, secure)
	if err != nil {
		t.Fatal(err)
	}
	owner := cooperative.New()
	defer owner.Close()
	catalog, err := p.AgentCatalog(owner, f.cwd, true)
	if err != nil {
		t.Fatal(err)
	}
	clear := func(ctx context.Context) error {
		receipt, err := owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error { return catalog.Clear(scope) })
		if err == nil {
			err = receipt.Wait()
		}
		return err
	}
	count := func(ctx context.Context, cwd string) (int, error) {
		value := 0
		receipt, err := owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			definitions, err := catalog.List(scope)
			if err != nil {
				return err
			}
			value = len(definitions.All)
			return nil
		})
		if err == nil {
			err = receipt.Wait()
		}
		return value, err
	}
	management, err := p.Plugins(NativePluginsManagementOptions{MCP: mcp, Options: options, Settings: editable, AgentsCount: count, ClearRuntimeCaches: clear, ReloadSession: func(context.Context, string) (protocol.JSONObject, error) {
		t.Error("This fixture has no sessions")
		return protocol.JSONObject{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	list, err := management.List(context.Background(), f.cwd)
	if err != nil {
		t.Fatal(err)
	}
	var listed []protocol.JSONObject
	_ = json.Unmarshal(list.Get("plugins"), &listed)
	if len(listed) != 1 || listed[0].String("id") != "sample@mkt" {
		t.Fatal("same installed plugin missing", list)
	}
	reload, err := management.Reload(context.Background(), f.cwd)
	if err != nil {
		t.Fatal(err)
	}
	var summary protocol.JSONObject
	_ = summary.UnmarshalJSON(reload.Get("summary"))
	if string(summary.Get("skills")) != "1" || string(summary.Get("agents")) != "7" || string(summary.Get("hooks")) != "1" {
		t.Fatal("actual shared component producers not used", reload)
	}
	registered, err := p.RegisteredPluginHooks(context.Background())
	if err != nil || registered == nil || !strings.Contains(string(registered.Get("Stop")), "fixture-only") {
		t.Fatal("hooks were counted without registering", registered, err)
	}
	if _, err = management.SetEnabled(context.Background(), "sample@mkt", false, "local", f.cwd); err != nil {
		t.Fatal(err)
	}
	editable.DrainBackground()
	local, err := os.ReadFile(filepath.Join(f.cwd, ".claude", "settings.local.json"))
	if err != nil || !strings.Contains(string(local), `"sample@mkt": false`) {
		t.Fatal("real local settings writer not used", string(local), err)
	}
	ignore, err := os.ReadFile(filepath.Join(f.home, ".config", "git", "ignore"))
	if err != nil || !strings.Contains(string(ignore), "**/.claude/settings.local.json") {
		t.Fatal("local Git-ignore continuation absent", string(ignore), err)
	}
	registered, err = p.RegisteredPluginHooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if registered != nil && strings.Contains(string(registered.Get("Stop")), "fixture-only") {
		t.Fatal("disabled plugin hooks remained registered", registered)
	}
	definitions, err := count(context.Background(), f.cwd)
	if err != nil || definitions != 6 {
		t.Fatal("disabled plugin remained in same agent producer", definitions, err)
	}
	if _, err = management.SetEnabled(context.Background(), "sample@mkt", true, "local", f.cwd); err != nil {
		t.Fatal(err)
	}
	registered, err = p.RegisteredPluginHooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if registered != nil && strings.Contains(string(registered.Get("Stop")), "fixture-only") {
		t.Fatal("new hooks registered before explicit reload", registered)
	}
	if _, err = management.Reload(context.Background(), f.cwd); err != nil {
		t.Fatal(err)
	}
	registered, err = p.RegisteredPluginHooks(context.Background())
	if err != nil || registered == nil || !strings.Contains(string(registered.Get("Stop")), "fixture-only") {
		t.Fatal("explicit reload failed actual hook swap", registered, err)
	}
}
