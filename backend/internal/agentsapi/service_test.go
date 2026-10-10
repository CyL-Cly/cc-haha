package agentsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"go.yaml.in/yaml/v3"
)

type disk struct{ root string }

func (f disk) Stat(path string) (fs.FileInfo, error)      { return os.Stat(path) }
func (f disk) Lstat(path string) (fs.FileInfo, error)     { return os.Lstat(path) }
func (f disk) ReadFile(path string) ([]byte, error)       { return os.ReadFile(path) }
func (f disk) ReadDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }
func (f disk) Realpath(path string) (string, error)       { return filepath.EvalSymlinks(path) }
func (f disk) Identity(info fs.FileInfo) string {
	value := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if value.Kind() == reflect.Struct {
		device, inode := value.FieldByName("Dev"), value.FieldByName("Ino")
		if device.IsValid() && inode.IsValid() {
			return fmt.Sprint(device.Interface(), ":", inode.Interface())
		}
	}
	return info.Name()
}
func raw(value any) json.RawMessage { valueJSON, _ := json.Marshal(value); return valueJSON }
func object(value any) protocol.JSONObject {
	var out protocol.JSONObject
	if err := out.UnmarshalJSON(raw(value)); err != nil {
		panic(err)
	}
	return out
}
func sourceAPI(t *testing.T, cases []map[string]any) []map[string]any {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agents-api.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = home
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_AGENTS_FIXTURE=1"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"cases": cases}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Actual Source agents API %v %s", err, stderr.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(output, &rows); err != nil {
		t.Fatalf("Invalid Source observations %v %s", err, output)
	}
	return rows
}

type fixture struct {
	service                 *Service
	home, config, workspace string
	catalog                 *agentcatalog.Service
	owner                   *cooperative.Owner
}

func nativeFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	f := &fixture{home: home, config: filepath.Join(home, ".claude"), workspace: filepath.Join(home, "workspace"), owner: cooperative.New()}
	t.Cleanup(f.owner.Close)
	for _, path := range []string{f.config, filepath.Join(f.workspace, ".git"), filepath.Join(home, "managed")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(f.config, "settings.json"), []byte("{}"), 0600)
	files := disk{home}
	catalogs := map[string]*agentcatalog.Service{}
	sourceSettings := func(cwd, source string) (*protocol.JSONObject, error) {
		path := ""
		switch source {
		case "userSettings":
			path = filepath.Join(f.config, "settings.json")
		case "projectSettings":
			path = filepath.Join(cwd, ".claude", "settings.json")
		case "localSettings":
			path = filepath.Join(cwd, ".claude", "settings.local.json")
		case "policySettings":
			path = filepath.Join(home, "managed", "managed-settings.json")
		}
		if path == "" {
			return nil, nil
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var value protocol.JSONObject
		if value.UnmarshalJSON(data) != nil {
			return nil, nil
		}
		return &value, nil
	}
	catalogFor := func(cwd string) (*agentcatalog.Service, error) {
		if existing := catalogs[cwd]; existing != nil {
			return existing, nil
		}
		created, err := agentcatalog.New(agentcatalog.Options{Owner: f.owner, Files: files, Home: home, CWD: cwd, ProjectRoot: f.workspace, ConfigHome: f.config, ManagedRoot: filepath.Join(home, "managed"), EnabledSources: []string{"userSettings", "projectSettings", "localSettings", "policySettings"}, Environment: func(key string) string {
			switch key {
			case "USER_TYPE":
				return "external"
			case "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH":
				return "1"
			case "CLAUDE_CODE_ENTRYPOINT":
				return "claude-desktop"
			}
			return ""
		}, Settings: func(source string) (*protocol.JSONObject, error) { return sourceSettings(cwd, source) }, Now: time.Now, NonInteractive: true, Plugins: func(*cooperative.Scope) ([]agentcatalog.Definition, error) {
			for _, directory := range []string{filepath.Join(f.config, "plugins"), filepath.Join(cwd, ".claude", "plugins")} {
				entries, err := os.ReadDir(directory)
				if err != nil && !os.IsNotExist(err) {
					return nil, err
				}
				if len(entries) > 0 {
					return nil, fmt.Errorf("Unexpected active plugins in the explicitly empty fixture")
				}
			}
			return nil, nil
		}})
		if err == nil {
			catalogs[cwd] = created
		}
		return created, err
	}
	settingsService, err := settings.New(settings.Options{Store: storage.New(storage.Options{}), HomeDir: home, ConfigDir: f.config, ProjectRoot: f.workspace, Environment: func() map[string]string { return map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": f.config} }, EnsureMigrated: func(context.Context, string) error { return nil }, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, GitIgnore: func(context.Context, string, string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = New(Options{HomeDir: home, ConfigDir: f.config, WorkDir: f.workspace, Load: func(ctx context.Context, cwd string) (*agentcatalog.Result, error) {
		catalog, err := catalogFor(cwd)
		if err != nil {
			return nil, err
		}
		var result *agentcatalog.Result
		err = f.owner.Run(ctx, func(scope *cooperative.Scope) error { result, err = catalog.List(scope); return err })
		return result, err
	}, AvailableTools: func(context.Context, string) ([]string, error) { return []string{"Write", "Read", "Bash"}, nil }, Invalidate: func(ctx context.Context) error {
		return f.owner.Run(ctx, func(scope *cooperative.Scope) error {
			for _, catalog := range catalogs {
				if err := catalog.Clear(scope); err != nil {
					return err
				}
			}
			return nil
		})
	}, Locked: func(context.Context) (bool, error) {
		policy, err := sourceSettings(f.workspace, "policySettings")
		if err != nil || policy == nil {
			return false, err
		}
		if string(policy.Get("strictPluginOnlyCustomization")) == "true" {
			return true, nil
		}
		var surfaces []string
		_ = json.Unmarshal(policy.Get("strictPluginOnlyCustomization"), &surfaces)
		for _, surface := range surfaces {
			if surface == "agents" {
				return true, nil
			}
		}
		return false, nil
	}, UpdateOverride: settingsService.UpdateBuiltInAgentOverride, Reload: func(context.Context, string) (protocol.JSONObject, error) {
		return object(map[string]any{"applied": false, "reason": "not_running", "commands": 0, "agents": 0, "plugins": 0, "mcpServers": 0, "errors": 0}), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) expand(text string) string {
	return strings.NewReplacer("$HOME", f.home, "$CONFIG", f.config, "$WORKSPACE", f.workspace).Replace(text)
}
func (f *fixture) seed(t *testing.T, row map[string]any) {
	t.Helper()
	if row["reset"] == true {
		_ = os.RemoveAll(filepath.Join(f.config, "agents"))
		_ = os.RemoveAll(filepath.Join(f.workspace, ".claude"))
		value := row["settings"]
		if value == nil {
			value = map[string]any{}
		}
		if err := os.WriteFile(filepath.Join(f.config, "settings.json"), raw(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if rows, ok := row["files"].([]any); ok {
		for _, value := range rows {
			file := value.(map[string]any)
			path := f.expand(file["path"].(string))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if link, ok := file["symlink"].(string); ok {
				if err := os.Symlink(f.expand(link), path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(f.expand(file["content"].(string))), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if row["clear"] == true {
		if err := f.service.invalidate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
func (f *fixture) snapshot(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range []string{f.home} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type().IsRegular() && strings.HasSuffix(path, ".md") {
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				out[strings.Replace(path, f.home, "$HOME", 1)] = string(raw)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func decoded(value any) any { var out any; _ = json.Unmarshal(raw(value), &out); return out }
func TestAgentsAPIAgainstActualSource(t *testing.T) {
	create := map[string]any{"name": "reviewer", "description": "review project", "systemPrompt": "use real tools to inspect", "model": "deepseek-flash", "tools": []string{"Read", "Bash"}, "effort": " HIGH ", "color": "blue"}
	call := func(method, path string, body any) map[string]any {
		row := map[string]any{"action": "api", "method": method, "path": path}
		if body != nil {
			row["body"] = body
		}
		return row
	}
	rows := []map[string]any{
		call("GET", "/api/agents", nil), call("POST", "/api/agents", create), call("GET", "/api/agents/reviewer", nil), call("GET", "/api/agents", nil), call("POST", "/api/agents", create),
		call("PUT", "/api/agents/reviewer", map[string]any{"description": "new description", "model": nil, "tools": []string{"*"}, "color": nil, "effort": -13, "systemPrompt": ""}),
		call("GET", "/api/agents/reviewer", nil), call("DELETE", "/api/agents/reviewer?scope=user", nil), call("GET", "/api/agents/reviewer", nil),
		call("PUT", "/api/agents/Explore/override", map[string]any{"model": " official-model ", "effort": "xhigh"}), call("GET", "/api/agents", nil), call("DELETE", "/api/agents/Explore/override", nil), call("DELETE", "/api/agents/Explore/override", nil),
		call("PUT", "/api/agents/general-purpose", map[string]any{"description": "cannot edit builtin"}), call("DELETE", "/api/agents/general-purpose", nil),
		call("POST", "/api/agents/reload", nil), call("POST", "/api/agents/reload?sessionId=unknown", nil), call("PATCH", "/api/agents", nil), call("PUT", "/api/agents/reload", map[string]any{}),
	}
	rows[0]["reset"] = true
	rows[0]["clear"] = true
	for _, body := range []any{nil, []any{}, true, map[string]any{}, map[string]any{"name": "Upper", "description": "d", "systemPrompt": "s"}, map[string]any{"scope": "bad"}, map[string]any{"scope": nil, "cwd": 4}, map[string]any{"unsupported": true}, map[string]any{"name": "ok", "description": "d", "systemPrompt": "s", "tools": []any{"Read", false}}, map[string]any{"name": "ok", "description": "d", "systemPrompt": "s", "color": "Blue"}, map[string]any{"name": "ok", "description": "d", "systemPrompt": "s", "effort": "invalid"}} {
		row := call("POST", "/api/agents", body)
		if body == nil {
			row["raw"] = "null"
		}
		rows = append(rows, row)
	}
	for _, body := range []any{map[string]any{"name": "changed"}, map[string]any{"target": ""}, map[string]any{"model": ""}, map[string]any{"description": ""}, map[string]any{"systemPrompt": nil}, map[string]any{"effort": 1.5}} {
		rows = append(rows, call("PUT", "/api/agents/reviewer", body))
	}
	for _, body := range []any{map[string]any{"scope": "user"}, map[string]any{"effort": true}, map[string]any{"effort": []any{1}}, map[string]any{"effort": nil}, map[string]any{"cwd": false, "model": "haiku"}} {
		rows = append(rows, call("PUT", "/api/agents/Explore/override", body))
	}
	rows = append(rows, call("GET", "/api/agents/Explore", nil), call("GET", "/api/agents/%FF", nil), call("DELETE", "/api/agents/missing?scope=bad", nil))
	nested := "---\nname: nested-reader\ndescription: nested original\ntools: Read\nfuture: # preserve unknown comment\n  nested: [one, two]\n---\n\npreserve old body\n"
	rows = append(rows, map[string]any{"action": "api", "method": "GET", "path": "/api/agents", "clear": true, "files": []any{map[string]any{"path": "$CONFIG/agents/deep/unrelated.md", "content": nested}}}, call("PUT", "/api/agents/nested-reader", map[string]any{"target": "$CONFIG/agents/deep/unrelated.md", "description": "updated nested"}), call("DELETE", "/api/agents/nested-reader?target=$CONFIG/agents/deep/unrelated.md&scope=user", nil))
	project := map[string]any{"scope": "project", "cwd": "$WORKSPACE", "name": "project-review", "description": "project reviewer", "systemPrompt": "review project"}
	rows = append(rows, call("POST", "/api/agents", project), call("GET", "/api/agents?cwd=$WORKSPACE", nil), call("PUT", "/api/agents/project-review", map[string]any{"scope": "project", "cwd": "$WORKSPACE", "model": "sonnet"}), call("DELETE", "/api/agents/project-review?scope=user&target=$WORKSPACE/.claude/agents/project-review.md", nil), call("DELETE", "/api/agents/project-review?scope=project&cwd=$WORKSPACE", nil))
	rows = append(rows,
		map[string]any{"action": "api", "method": "GET", "path": "/api/agents", "reset": true, "clear": true, "files": []any{map[string]any{"path": "$CONFIG/agents/anchored.md", "content": "---\nname: anchored\ndescription: &description original description # preserve comment\nfuture: *description\n---\n\noriginal body\n"}}},
		call("PUT", "/api/agents/anchored", map[string]any{"description": "new anchored description"}),
		call("GET", "/api/agents/anchored", nil),
		call("PUT", "/api/agents/missing/override", map[string]any{"model": ""}),
		call("POST", "/api/agents", map[string]any{"scope": "project", "cwd": "$WORKSPACE/missing", "name": "test", "description": "desc", "systemPrompt": "prompt"}),
		map[string]any{"action": "api", "method": "GET", "path": "/api/agents", "reset": true, "clear": true, "files": []any{map[string]any{"path": "$CONFIG/agents/existing.md", "content": "---\nname: different\ndescription: protected identity\n---\nkeep this\n"}}},
		call("PUT", "/api/agents/existing", map[string]any{"description": "must not touch mismatched identity"}),
		call("PUT", "/api/agents/different", map[string]any{"target": "$CONFIG/agents/existing.md", "model": "new-model"}),
		call("DELETE", "/api/agents/existing", nil),
		map[string]any{"action": "api", "method": "GET", "path": "/api/agents", "reset": true, "clear": true, "files": []any{map[string]any{"path": "$CONFIG/agents/bad.md", "content": "not YAML frontmatter\n"}, map[string]any{"path": "$CONFIG/agents/duplicate.md", "content": "---\nname: duplicate\ndescription: first\ndescription: second\n---\nkeep\n"}}},
		call("PUT", "/api/agents/bad", map[string]any{"model": "new"}),
		call("PUT", "/api/agents/duplicate", map[string]any{"model": "new"}),
		map[string]any{"action": "api", "method": "POST", "path": "/api/agents", "reset": true, "clear": true, "files": []any{map[string]any{"path": "$HOME/outside/untouched.md", "content": "---\nname: readonly\ndescription: external\n---\nprotected\n"}, map[string]any{"path": "$CONFIG/agents/readonly.md", "symlink": "$HOME/outside/untouched.md"}}, "body": map[string]any{"name": "readonly", "description": "must not overwrite", "systemPrompt": "must not write"}},
		call("PUT", "/api/agents/readonly", map[string]any{"model": "new"}),
		call("DELETE", "/api/agents/readonly", nil),
		map[string]any{"action": "api", "method": "POST", "path": "/api/agents", "reset": true, "clear": true, "files": []any{map[string]any{"path": "$HOME/external-agents/keep.md", "content": "---\nname: keep\ndescription: external\n---\nprotected\n"}, map[string]any{"path": "$CONFIG/agents", "symlink": "$HOME/external-agents"}}, "body": map[string]any{"name": "new", "description": "desc", "systemPrompt": "prompt"}},
		map[string]any{"action": "api", "method": "PUT", "path": "/api/agents/Explore/override", "reset": true, "clear": true, "files": []any{map[string]any{"path": "$HOME/managed/managed-settings.json", "content": "{\"strictPluginOnlyCustomization\":[\"agents\"]}"}}, "body": map[string]any{"model": ""}},
		call("DELETE", "/api/agents/Explore/override", nil),
		map[string]any{"action": "api", "method": "GET", "path": "/api/agents?cwd=$HOME/worktree", "reset": true, "clear": true, "files": []any{
			map[string]any{"path": "$HOME/managed/managed-settings.json", "content": "{}"},
			map[string]any{"path": "$HOME/worktree/.git", "content": "gitdir: $WORKSPACE/.git/worktrees/wt\n"},
			map[string]any{"path": "$WORKSPACE/.git/worktrees/wt/commondir", "content": "../..\n"},
			map[string]any{"path": "$WORKSPACE/.git/worktrees/wt/gitdir", "content": "$HOME/worktree/.git\n"},
			map[string]any{"path": "$WORKSPACE/.claude/agents/base.md", "content": "---\nname: base\ndescription: original main agent\n---\nmain prompt\n"},
		}},
		call("POST", "/api/agents", map[string]any{"scope": "project", "cwd": "$HOME/worktree", "name": "canonical-new", "description": "canonical creation", "systemPrompt": "inspect branch"}),
		call("PUT", "/api/agents/canonical-new", map[string]any{"scope": "project", "cwd": "$HOME/worktree", "target": "$WORKSPACE/.claude/agents/canonical-new.md", "color": "green"}),
		call("DELETE", "/api/agents/canonical-new?scope=project&cwd=$HOME/worktree&target=$WORKSPACE/.claude/agents/canonical-new.md", nil),
		map[string]any{"action": "api", "method": "GET", "path": "/api/agents?cwd=$HOME/worktree", "clear": true, "files": []any{map[string]any{"path": "$HOME/worktree/.claude/agents/local.md", "content": "---\nname: local\ndescription: worktree own\n---\nlocal prompt\n"}}},
		call("POST", "/api/agents", map[string]any{"scope": "project", "cwd": "$HOME/worktree", "name": "worktree-new", "description": "own worktree creation", "systemPrompt": "inspect worktree"}),
		call("POST", "/api/agents", map[string]any{"scope": "project", "cwd": "$HOME", "name": "collision", "description": "must not collide", "systemPrompt": "keep protected"}),
		call("POST", "/api/agents", map[string]any{"name": "huge-effort", "description": "integer number boundaries", "systemPrompt": "inspect only", "effort": 1e30}),
		call("PUT", "/api/agents/huge-effort", map[string]any{"effort": -1e100}),
		call("DELETE", "/api/agents/huge-effort", nil),
	)
	expected := sourceAPI(t, rows)
	f := nativeFixture(t)
	for index, row := range rows {
		t.Run(strconvIndex(index), func(t *testing.T) {
			f.seed(t, row)
			path := f.expand(row["path"].(string))
			requestURL, err := urlParse(path)
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(strings.Trim(requestURL.EscapedPath(), "/"), "/")
			var body []byte
			if value, ok := row["raw"].(string); ok {
				body = []byte(f.expand(value))
			} else if value, ok := row["body"]; ok {
				body = []byte(f.expand(string(raw(value))))
			}
			status, value, err := f.service.Handle(context.Background(), row["method"].(string), requestURL, parts, body)
			if err != nil {
				if api, ok := err.(*APIError); ok {
					status = api.StatusCode
					value = map[string]string{"error": api.Code, "message": api.Message}
				} else {
					status = 500
					value = map[string]string{"error": "INTERNAL_ERROR", "message": "An unexpected error occurred"}
				}
			}
			actualJSON := strings.ReplaceAll(string(raw(value)), f.home, "$HOME")
			var actualBody any
			_ = json.Unmarshal([]byte(actualJSON), &actualBody)
			wantBody := expected[index]["body"]
			if want, ok := wantBody.(map[string]any); ok {
				delete(want, "availableTools")
			}
			if got, ok := actualBody.(map[string]any); ok {
				if tools, exists := got["availableTools"]; exists {
					if !reflect.DeepEqual(tools, []any{"Bash", "Read", "Write"}) {
						t.Fatalf("Actual enabled-tool callback must be sorted %v", tools)
					}
					delete(got, "availableTools")
				}
			}
			if status != int(expected[index]["status"].(float64)) || !reflect.DeepEqual(wantBody, actualBody) {
				t.Fatalf("case%d status Source%d Go%d\nSource %s\nGo %s", index, int(expected[index]["status"].(float64)), status, raw(wantBody), raw(actualBody))
			}
			actualFiles := f.snapshot(t)
			expectedFiles := expected[index]["files"].(map[string]any)
			if len(actualFiles) != len(expectedFiles) {
				t.Fatalf("Different actual files Source %v Go %v", expectedFiles, actualFiles)
			}
			for path, value := range expectedFiles {
				native := actualFiles[path]
				sourceText := value.(string)
				sourceHeader, sourceBody := parseMarkdown(t, sourceText)
				nativeHeader, nativeBody := parseMarkdown(t, native)
				if !reflect.DeepEqual(sourceHeader, nativeHeader) || sourceBody != nativeBody {
					t.Fatalf("Persisted Markdown differs %s\nSource %s\nGo %s", path, sourceText, native)
				}
				if strings.Contains(sourceText, "preserve unknown comment") && !strings.Contains(native, "preserve unknown comment") {
					t.Fatal("Unknown YAML comments were lost")
				}
			}
			data, _ := os.ReadFile(filepath.Join(f.config, "settings.json"))
			var settings any
			_ = json.Unmarshal(data, &settings)
			if !reflect.DeepEqual(expected[index]["settings"], settings) {
				t.Fatalf("Unknown settings lost Source %s Go %s", raw(expected[index]["settings"]), raw(settings))
			}
		})
	}
}
func strconvIndex(index int) string          { return fmt.Sprint(index) }
func urlParse(path string) (*url.URL, error) { return url.Parse("http://127.0.0.1:3000" + path) }
func parseMarkdown(t *testing.T, text string) (any, string) {
	t.Helper()
	indexes := frontmatter.FindStringSubmatchIndex(text)
	if indexes == nil {
		return text, ""
	}
	var value any
	if err := yaml.Unmarshal([]byte(text[indexes[2]:indexes[3]]), &value); err != nil {
		return text, ""
	}
	return value, text[indexes[1]:]
}

func TestAgentAtomicCreateConcurrencyAgainstActualSource(t *testing.T) {
	const callers = 32
	agent := map[string]any{"name": "parallel", "description": "atomically published", "systemPrompt": "inspect scoped files"}
	expected := sourceAPI(t, []map[string]any{{"action": "concurrentCreate", "count": callers, "agent": agent}})[0]
	f := nativeFixture(t)
	fields := object(agent)
	var successes atomic.Int64
	failures := make(chan any, callers)
	var done sync.WaitGroup
	done.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer done.Done()
			_, err := f.service.CreateFile(context.Background(), fields, MutationContext{Scope: "user"})
			if err == nil {
				successes.Add(1)
				return
			}
			var api *APIError
			if !errors.As(err, &api) {
				t.Error(err)
				return
			}
			failures <- map[string]any{"status": float64(api.StatusCode), "code": api.Code, "message": api.Message}
		}()
	}
	done.Wait()
	close(failures)
	actualFailures := []any{}
	for failure := range failures {
		actualFailures = append(actualFailures, failure)
	}
	if successes.Load() != int64(expected["successes"].(float64)) || !reflect.DeepEqual(actualFailures, expected["failures"]) {
		t.Fatalf("Concurrent create differs Source %s Go successes=%d failures=%s", raw(expected), successes.Load(), raw(actualFailures))
	}
	files := f.snapshot(t)
	for path, sourceValue := range expected["files"].(map[string]any) {
		sourceHeader, sourceBody := parseMarkdown(t, sourceValue.(string))
		goHeader, goBody := parseMarkdown(t, files[path])
		if !reflect.DeepEqual(sourceHeader, goHeader) || sourceBody != goBody {
			t.Fatal("Published file was incomplete")
		}
	}
	info, err := os.Stat(filepath.Join(f.config, "agents", "parallel.md"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("Expected private published mode0600, got %v %v", info, err)
	}
	entries, _ := os.ReadDir(filepath.Join(f.config, "agents"))
	if len(entries) != 1 {
		t.Fatalf("Temporary files leaked %v", entries)
	}
}
func TestAgentAtomicReplaceRejectsStaleIdentity(t *testing.T) {
	f := nativeFixture(t)
	fields := object(map[string]any{"name": "stale", "description": "initial", "systemPrompt": "original body"})
	result, err := f.service.CreateFile(context.Background(), fields, MutationContext{Scope: "user"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := readFile(result.Target)
	if err != nil {
		t.Fatal(err)
	}
	external := []byte("---\nname: stale\ndescription: externally changed\n---\n\nexternal contents\n")
	if err = os.WriteFile(result.Target, external, 0600); err != nil {
		t.Fatal(err)
	}
	err = atomicReplace(result.Target, []byte("must not overwrite"), parsed.identity)
	var api *APIError
	if !errors.As(err, &api) || api.StatusCode != 409 || api.Code != "CONFLICT" {
		t.Fatalf("Expected Source stale-target conflict, got %v", err)
	}
	after, _ := os.ReadFile(result.Target)
	if !bytes.Equal(after, external) {
		t.Fatal("Stale replacement modified user data")
	}
}
