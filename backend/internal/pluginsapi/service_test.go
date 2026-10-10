package pluginsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginmcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func sourcePlugins(t *testing.T, cases []map[string]any) []map[string]any {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun unavailable")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugins-api.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_PLUGINS_FIXTURE=1"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"cases": cases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Actual Source Plugins %v %s", err, stderr.String())
	}
	var rows []map[string]any
	if err = json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("Source observations %v %s", err, out)
	}
	return rows
}

type fixture struct {
	service                 *Service
	home, config, workspace string
	loader                  *pluginregistry.Loader
	state                   *clisettings.State
	settings                *clisettings.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{home: t.TempDir()}
	f.config = filepath.Join(f.home, ".claude")
	f.workspace = filepath.Join(f.home, "workspace")
	for _, path := range []string{f.config, f.workspace, filepath.Join(f.home, "managed")} {
		_ = os.MkdirAll(path, 0700)
	}
	_ = os.WriteFile(filepath.Join(f.config, "settings.json"), []byte("{}"), 0600)
	clock := func() time.Time { return time.UnixMilli(1791244800000) }
	f.state, _ = clisettings.NewState(func() float64 { return float64(clock().UnixMilli()) })
	var err error
	f.settings, err = clisettings.NewService(clisettings.Options{State: f.state, ConfigHome: f.config, OriginalCWD: f.workspace, Profile: clisettings.NativeProcessProfile, Now: func() float64 { return float64(clock().UnixMilli()) }, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	read := func(ctx context.Context, scope, cwd string) (protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return rec(), err
		}
		if scope == "managed" {
			data, err := os.ReadFile(filepath.Join(f.home, "managed", "managed-settings.json"))
			if os.IsNotExist(err) {
				return rec(), nil
			}
			return obj(data), err
		}
		source := map[string]string{"user": "userSettings", "project": "projectSettings", "local": "localSettings"}[scope]
		value, err := f.settings.GetEditableSource(source, cwd)
		if value == nil {
			return rec(), err
		}
		return value.Clone(), err
	}
	write := func(ctx context.Context, scope, cwd, id string, enabled *bool) error {
		source := map[string]string{"user": "userSettings", "project": "projectSettings", "local": "localSettings"}[scope]
		if scope == "user" {
			cwd = ""
		}
		patch := clisettings.Patch{Object: rec(field("enabledPlugins", rec()))}
		if enabled != nil {
			patch.Object = rec(field("enabledPlugins", rec(field(id, *enabled))))
		} else {
			patch.UndefinedPaths = [][]json.RawMessage{{raw("enabledPlugins"), raw(id)}}
		}
		result, err := f.settings.UpdateEditableSource(source, cwd, patch)
		if err != nil {
			return err
		}
		return result.Error
	}
	merged := func(ctx context.Context, cwd string) (protocol.JSONObject, error) {
		if cwd == "" {
			cwd = f.workspace
		}
		enabled := rec()
		settings := rec()
		for _, scope := range []string{"user", "project", "local", "managed"} {
			current, err := read(ctx, scope, cwd)
			if err != nil {
				return rec(), err
			}
			for _, p := range current.OwnProperties() {
				_ = settings.SetPropertyJSON(p.NameJSON, p.Value)
			}
			for _, p := range obj(current.Get("enabledPlugins")).OwnProperties() {
				_ = enabled.SetPropertyJSON(p.NameJSON, p.Value)
			}
		}
		settings.Set("enabledPlugins", raw(enabled))
		return settings, nil
	}
	installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(f.config, "plugins"), Store: storage.New(storage.Options{}), Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	materializer, err := pluginregistry.NewMaterializer(pluginregistry.MaterializerOptions{Directories: pluginregistry.Directories{Primary: filepath.Join(f.config, "plugins")}, CWD: f.workspace, Environment: map[string]string{"PATH": os.Getenv("PATH")}, Installed: installed, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	f.loader, err = pluginregistry.New(pluginregistry.Options{HomeDir: f.home, ConfigDir: f.config, CWD: f.workspace, Environment: func(string) string { return "" }, Settings: merged, Policy: func(ctx context.Context) (protocol.JSONObject, error) { return read(ctx, "managed", "") }, AddDirEnabled: func(context.Context) (protocol.JSONObject, error) { return rec(), nil }, Builtins: pluginregistry.NewBuiltinRegistry(), SettingsState: f.state, Installed: installed, Prepare: materializer.Prepare})
	if err != nil {
		t.Fatal(err)
	}
	secure, err := securestorage.New(securestorage.Options{Home: f.home, ConfigDir: f.config, Platform: "linux", Environment: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	writeUser := func(ctx context.Context, patch clisettings.Patch) error {
		result, err := f.settings.UpdateEditableSource("userSettings", "", patch)
		if err != nil {
			return err
		}
		return result.Error
	}
	options, err := pluginoptions.New(pluginoptions.Options{Secure: secure, Settings: func(ctx context.Context) (protocol.JSONObject, error) { return merged(ctx, "") }, WriteSettings: writeUser})
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := pluginmcp.New(pluginmcp.Options{Home: f.home, ConfigDir: f.config, CWD: f.workspace, Platform: "linux", Environment: func(string) (string, bool) { return "", false }, PluginOptions: options, Secure: secure, Settings: func(ctx context.Context) (protocol.JSONObject, error) { return merged(ctx, "") }, WriteSettings: writeUser, LoadMCPB: func(context.Context, string, string, string, func(string)) (pluginmcp.MCPBResult, error) {
		return pluginmcp.MCPBResult{}, errors.New("Unexpected MCPB in private fixture")
	}})
	if err != nil {
		t.Fatal(err)
	}
	load := func(ctx context.Context, full bool) (pluginregistry.Result, error) {
		if full {
			return f.loader.LoadFull(ctx)
		}
		return f.loader.LoadCached(ctx)
	}
	clear := func(context.Context) error {
		f.state.ResetSettingsCache()
		f.loader.Clear()
		installed.Clear()
		options.Clear()
		return nil
	}
	f.service, err = New(Options{WorkDir: f.workspace, PluginsDir: filepath.Join(f.config, "plugins"), Installed: installed, Materializer: materializer, Load: load, Hydrate: func(ctx context.Context, result *pluginregistry.Result) error {
		for i := range result.Enabled {
			p := &result.Enabled[i]
			if p.MCPServers == nil {
				servers, err := mcp.LoadPluginMCPServers(ctx, obj(raw(p)), &result.Errors)
				if err != nil {
					return err
				}
				p.MCPServers = servers
			}
			if p.LSPServers == nil {
				servers, err := LoadLSP(ctx, *p, &result.Errors)
				if err != nil {
					return err
				}
				p.LSPServers = servers
			}
		}
		return nil
	}, ReadSettings: read, WriteEnabled: write, EditableEnabled: func(ctx context.Context) (map[string]bool, error) {
		out := map[string]bool{}
		for _, scope := range []string{"managed", "user", "project", "local"} {
			settings, err := read(ctx, scope, "")
			if err != nil {
				return nil, err
			}
			for _, key := range properties(obj(settings.Get("enabledPlugins"))) {
				v := obj(settings.Get("enabledPlugins")).Get(key)
				if strings.Contains(key, "@") {
					if string(v) == "true" {
						out[key] = true
					} else if string(v) == "false" {
						delete(out, key)
					}
				}
			}
		}
		return out, nil
	}, ClearCaches: clear, KnownMarketplaces: func(ctx context.Context) (protocol.JSONObject, error) {
		return ReadKnownMarketplaces(ctx, filepath.Join(f.config, "plugins"))
	}, LookupPlugin: func(ctx context.Context, id string) (*MarketplacePlugin, error) {
		return LookupCachedPlugin(ctx, filepath.Join(f.config, "plugins"), id)
	}, DeleteOptions: options.Delete, IsBuiltin: func(id string) bool { return strings.HasSuffix(id, "@builtin") }, BuiltinDetail: func(string) (protocol.JSONObject, error) { return rec(), errors.New("No stock builtins declared") }, SkillsCount: func(ctx context.Context) (int, error) {
		state, err := load(ctx, false)
		for _, p := range state.Enabled {
			if p.SkillsPath != "" || len(p.SkillsPaths) > 0 {
				return 0, errors.New("Reload fixture must bind actual skills producer for nonempty skills")
			}
		}
		return 0, err
	}, AgentsCount: func(ctx context.Context, cwd string) (int, error) {
		state, err := load(ctx, false)
		for _, p := range state.Enabled {
			if p.AgentsPath != "" || len(p.AgentsPaths) > 0 {
				return 0, errors.New("Reload fixture must bind actual agents producer for custom agents")
			}
		}
		return len(agentbuiltinmetadata.Stock()), err
	}, LoadHooks: func(ctx context.Context) error { _, err := load(ctx, false); return err }, ReloadSession: func(context.Context, string) (protocol.JSONObject, error) {
		return rec(field("applied", false), field("reason", "session-not-running")), nil
	}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) expand(s string) string {
	return strings.NewReplacer("$HOME", f.home, "$WORKSPACE", f.workspace, "$CONFIG", f.config).Replace(s)
}
func (f *fixture) seed(t *testing.T, row map[string]any) {
	t.Helper()
	if row["reset"] == true {
		_ = os.RemoveAll(filepath.Join(f.config, "plugins"))
		_ = os.RemoveAll(filepath.Join(f.workspace, ".claude"))
		_ = os.RemoveAll(filepath.Join(f.home, "market"))
		value := row["settings"]
		if value == nil {
			value = map[string]any{}
		}
		_ = os.WriteFile(filepath.Join(f.config, "settings.json"), raw(value), 0600)
		_ = os.WriteFile(filepath.Join(f.home, "managed", "managed-settings.json"), []byte("{}"), 0600)
	}
	if files, ok := row["files"].([]map[string]any); ok {
		for _, file := range files {
			path := f.expand(file["path"].(string))
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			if link, ok := file["symlink"].(string); ok {
				if err := os.Symlink(f.expand(link), path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte(f.expand(file["content"].(string))), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if repo, ok := row["git"].(string); ok {
		path := f.expand(repo)
		for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"}} {
			cmd := exec.Command("/usr/bin/git", args...)
			cmd.Dir = path
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + f.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_DATE=2026-10-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-10-01T00:00:00Z"}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("private git %v %s", err, output)
			}
		}
	}
	f.state.ResetSettingsCache()
	if row["clear"] == true {
		_ = f.service.options.ClearCaches(context.Background())
	}
}

var isoPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z`)

func normalize(value any, home string) any {
	data := strings.ReplaceAll(string(raw(value)), home, "$HOME")
	data = isoPattern.ReplaceAllString(data, "$$DATE")
	var v any
	_ = json.Unmarshal([]byte(data), &v)
	return v
}
func (f *fixture) snapshot(t *testing.T) map[string]any {
	t.Helper()
	files := map[string]any{}
	err := filepath.WalkDir(f.home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !d.Type().IsRegular() || strings.HasSuffix(path, ".claude.json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		key := strings.ReplaceAll(path, f.home, "$HOME")
		if strings.HasSuffix(path, ".json") {
			var value any
			if err = json.Unmarshal(data, &value); err == nil {
				files[key] = normalize(value, f.home)
			} else {
				files[key] = strings.ReplaceAll(string(data), f.home, "$HOME")
			}
		} else {
			files[key] = strings.ReplaceAll(string(data), f.home, "$HOME")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
func (f *fixture) request(t *testing.T, row map[string]any) map[string]any {
	t.Helper()
	f.seed(t, row)
	u, err := url.Parse("http://127.0.0.1:3000" + f.expand(row["path"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte{}
	if value, ok := row["body"]; ok {
		body = []byte(f.expand(string(raw(value))))
	}
	if value, ok := row["raw"].(string); ok {
		body = []byte(f.expand(value))
	}
	status, value, err := f.service.Handle(context.Background(), row["method"].(string), u, strings.Split(strings.Trim(u.Path, "/"), "/"), body)
	if err != nil {
		var api *APIError
		if errors.As(err, &api) {
			status = api.StatusCode
			value = map[string]any{"error": api.Code, "message": api.Message}
		} else {
			status = 500
			value = map[string]any{"error": "INTERNAL_ERROR", "message": "An unexpected error occurred"}
		}
	}
	return map[string]any{"status": float64(status), "body": normalize(value, f.home), "files": normalize(f.snapshot(t), f.home)}
}
func compareCases(t *testing.T, cases []map[string]any) {
	t.Helper()
	expected := sourcePlugins(t, cases)
	f := newFixture(t)
	for i, row := range cases {
		got := f.request(t, row)
		if !reflect.DeepEqual(got, expected[i]) {
			t.Fatalf("Case %d %s %s\nGo=%s\nSource=%s", i, row["method"], row["path"], raw(got), raw(expected[i]))
		}
	}
}
func seedPlugin(id, scope, project string, version string) []map[string]any {
	name, market := splitID(id)
	path := "$CONFIG/plugins/cache/" + market + "/" + name + "/" + version
	entry := map[string]any{"scope": scope, "installPath": path, "version": version, "installedAt": "2026-10-01T00:00:00.000Z", "lastUpdated": "2026-10-01T00:00:00.000Z"}
	if project != "" {
		entry["projectPath"] = project
	}
	return []map[string]any{{"path": "$CONFIG/plugins/known_marketplaces.json", "content": string(raw(map[string]any{market: map[string]any{"source": map[string]any{"source": "directory", "path": "$HOME/market"}, "installLocation": "$HOME/market", "lastUpdated": "2026-10-01T00:00:00.000Z"}}))}, {"path": "$HOME/market/.claude-plugin/marketplace.json", "content": string(raw(map[string]any{"name": market, "owner": map[string]any{"name": "owner"}, "plugins": []any{map[string]any{"name": name, "source": "./plugins/" + name}}}))}, {"path": "$CONFIG/plugins/installed_plugins.json", "content": string(raw(map[string]any{"version": 2, "plugins": map[string]any{id: []any{entry}}}))}, {"path": path + "/.claude-plugin/plugin.json", "content": string(raw(map[string]any{"name": name, "version": version, "description": "Plugin description", "author": map[string]any{"name": "Author"}}))}, {"path": "$HOME/market/plugins/" + name + "/.claude-plugin/plugin.json", "content": string(raw(map[string]any{"name": name, "version": version, "description": "Plugin description", "author": map[string]any{"name": "Author"}}))}}
}
func seedLocalComponents(files []map[string]any) []map[string]any {
	for _, file := range append([]map[string]any{}, files...) {
		path := file["path"].(string)
		prefix := "$CONFIG/plugins/cache/mkt/sample/1.0.0/"
		if strings.HasPrefix(path, prefix) && !strings.HasSuffix(path, ".claude-plugin/plugin.json") {
			clone := map[string]any{}
			for key, value := range file {
				clone[key] = value
			}
			clone["path"] = "$HOME/market/plugins/sample/" + strings.TrimPrefix(path, prefix)
			files = append(files, clone)
		}
	}
	return files
}
func TestPluginHTTPValidationAgainstActualSource(t *testing.T) {
	cases := []map[string]any{}
	add := func(method, path string, body any) {
		row := map[string]any{"method": method, "path": path}
		if body != nil {
			row["body"] = body
		}
		cases = append(cases, row)
	}
	add("GET", "/api/plugins/detail", nil)
	add("GET", "/api/plugins/unknown", nil)
	add("PUT", "/api/plugins/enable", map[string]any{"id": "a@b"})
	for _, value := range []any{nil, []any{}, true, "x", 17} {
		cases = append(cases, map[string]any{"method": "POST", "path": "/api/plugins/enable", "raw": string(raw(value))})
	}
	cases = append(cases, map[string]any{"method": "POST", "path": "/api/plugins/enable", "raw": "{"})
	for _, body := range []map[string]any{{}, {"id": " "}, {"id": 9}, {"id": "x@b", "other": true}, {"id": "x@b", "scope": "managed"}, {"id": "x@b", "cwd": "relative"}, {"id": "x@b", "cwd": "$HOME/missing"}, {"id": "x@b", "cwd": 3}, {"id": "x@b", "scope": "project"}, {"id": "x@b", "scope": false}, {"id": strings.Repeat("界", 513)}} {
		add("POST", "/api/plugins/enable", body)
	}
	add("POST", "/api/plugins/update", map[string]any{"id": "x@b"})
	add("POST", "/api/plugins/uninstall", map[string]any{"id": "x@b", "scope": "user", "keepData": nil})
	add("POST", "/api/plugins/unknown", map[string]any{"id": "x@b"})
	compareCases(t, cases)
}
func TestPluginStateAndMutationAgainstActualSource(t *testing.T) {
	files := seedPlugin("sample@mkt", "user", "", "1.0.0")
	files = append(files, []map[string]any{{"path": "$CONFIG/plugins/data/sample-mkt/persist.txt", "content": "real persistent data"}, {"path": "$CONFIG/plugins/cache/mkt/sample/1.0.0/commands/group/task.MD", "content": "---\ndescription: Command description\n---\nRun task"}, {"path": "$CONFIG/plugins/cache/mkt/sample/1.0.0/commands/leaf/SKILL.md", "content": "# Leaf command"}, {"path": "$CONFIG/plugins/cache/mkt/sample/1.0.0/commands/leaf/nested/skip.md", "content": "Should stop at skill dir"}, {"path": "$CONFIG/plugins/cache/mkt/sample/1.0.0/agents/nested/worker.md", "content": "---\nname: custom-worker\nwhen-to-use:   Specific work\n---\n# fallback"}, {"path": "$CONFIG/plugins/cache/mkt/sample/1.0.0/skills/test/SKILL.md", "content": "---\nname: Friendly skill\ndescription: Skill description\nversion: 7\n---\nBody"}, {"path": "$CONFIG/plugins/cache/mkt/sample/1.0.0/.mcp.json", "content": "{\"stdio\":{\"command\":\"fake\",\"args\":[\"--test\",\"\"]},\"remote\":{\"type\":\"http\",\"url\":\"http://127.0.0.1:1/mcp\"}}"}, {"path": "$CONFIG/plugins/cache/mkt/sample/1.0.0/.lsp.json", "content": "{\"typescript\":{\"command\":\"tsserver\",\"extensionToLanguage\":{\".ts\":\"typescript\"}}}"}}...)
	files = seedLocalComponents(files)
	cases := []map[string]any{{"method": "GET", "path": "/api/plugins", "reset": true, "clear": true, "settings": map[string]any{"future": map[string]any{"keep": true}, "enabledPlugins": map[string]any{"sample@mkt": true}}, "files": files}, {"method": "GET", "path": "/api/plugins/detail?id=sample%40mkt"}, {"method": "GET", "path": "/api/plugins/detail?id=missing%40mkt"}, {"method": "POST", "path": "/api/plugins/enable", "body": map[string]any{"id": "sample@mkt"}}, {"method": "POST", "path": "/api/plugins/disable", "body": map[string]any{"id": "sample"}}, {"method": "GET", "path": "/api/plugins"}, {"method": "POST", "path": "/api/plugins/disable", "body": map[string]any{"id": "sample@mkt", "scope": "user"}}, {"method": "POST", "path": "/api/plugins/enable", "body": map[string]any{"id": "sample@mkt", "scope": "project", "cwd": "$WORKSPACE"}}, {"method": "POST", "path": "/api/plugins/disable", "body": map[string]any{"id": "sample@mkt", "scope": "user"}}, {"method": "POST", "path": "/api/plugins/disable", "body": map[string]any{"id": "sample@mkt", "scope": "local", "cwd": "$WORKSPACE"}}, {"method": "POST", "path": "/api/plugins/uninstall", "body": map[string]any{"id": "sample@mkt", "scope": "project", "cwd": "$WORKSPACE"}}, {"method": "POST", "path": "/api/plugins/uninstall", "body": map[string]any{"id": "sample@mkt", "scope": "user", "keepData": true}}, {"method": "GET", "path": "/api/plugins"}}
	compareCases(t, cases)
	expected := sourcePlugins(t, cases[:1])
	first := expected[0]["body"].(map[string]any)["plugins"].([]any)[0].(map[string]any)
	counts := first["componentCounts"].(map[string]any)
	for key, want := range map[string]float64{"commands": 2, "agents": 1, "skills": 1, "mcpServers": 2, "lspServers": 1} {
		if counts[key] != want {
			t.Fatalf("Source component fixture %s=%v expected %v", key, counts[key], want)
		}
	}
}
func TestPluginUpdateAndUninstallDataAgainstActualSource(t *testing.T) {
	files := seedPlugin("sample@mkt", "user", "", "1.0.0")
	files = append(files, map[string]any{"path": "$CONFIG/plugins/data/sample-mkt/state", "content": "data survive update"})
	cases := []map[string]any{{"method": "POST", "path": "/api/plugins/update", "body": map[string]any{"id": "sample@mkt", "scope": "user"}, "reset": true, "clear": true, "files": files, "settings": map[string]any{"enabledPlugins": map[string]any{"sample@mkt": true}, "pluginConfigs": map[string]any{"sample@mkt": map[string]any{"options": map[string]any{"count": 9}}, "other@mkt": map[string]any{"options": map[string]any{"keep": true}}}}}, {"method": "POST", "path": "/api/plugins/update", "body": map[string]any{"id": "sample@mkt", "scope": "user"}, "files": []map[string]any{{"path": "$HOME/market/plugins/sample/.claude-plugin/plugin.json", "content": "{\"name\":\"sample\",\"version\":\"2.0.0\"}"}}}, {"method": "GET", "path": "/api/plugins/detail?id=sample%40mkt"}, {"method": "POST", "path": "/api/plugins/reload"}, {"method": "GET", "path": "/api/plugins/detail?id=sample%40mkt"}, {"method": "POST", "path": "/api/plugins/uninstall", "body": map[string]any{"id": "sample@mkt", "scope": "user"}}, {"method": "GET", "path": "/api/plugins"}}
	compareCases(t, cases)
}
func TestPluginLSPMetadataAgainstActualSource(t *testing.T) {
	cases := []map[string]any{}
	base := map[string]any{"name": "sample", "path": "$HOME/plugin", "source": "sample@mkt", "manifest": map[string]any{}}
	for _, content := range []string{`{"ts":{"command":"tsserver","extensionToLanguage":{".ts":"typescript"}}}`, `{"bad":{"command":"has spaces","extensionToLanguage":{}}}`, `{"bad":{"command":"x","extensionToLanguage":{"ts":"x"},"future":true}}`, `{`} {
		cases = append(cases, map[string]any{"action": "lsp", "plugin": base, "reset": true, "files": []map[string]any{{"path": "$HOME/plugin/.lsp.json", "content": content}}})
	}
	expected := sourcePlugins(t, cases)
	f := newFixture(t)
	for i, row := range cases {
		f.seed(t, row)
		var p pluginregistry.Loaded
		_ = json.Unmarshal([]byte(f.expand(string(raw(base)))), &p)
		failures := []protocol.JSONObject{}
		servers, err := LoadLSP(context.Background(), p, &failures)
		if err != nil {
			t.Fatal(err)
		}
		got := normalize(map[string]any{"servers": servers, "errors": failures}, f.home)
		if !reflect.DeepEqual(got, expected[i]) {
			t.Fatalf("LSP case %d\nGo %s\nSource %s", i, raw(got), raw(expected[i]))
		}
	}
}
func TestPluginMissingProducersCannotMasqueradeAsSuccess(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("unbound management service accepted")
	}
}
