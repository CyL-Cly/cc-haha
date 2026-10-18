package slashcommandsapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/slashcommandsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func TestSlashMenuListsActualFilesRootsLegacyAndCompiledAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "repo", "pkg")
	managed := filepath.Join(home, "managed")
	tmp := filepath.Join(home, "tmp")
	for _, path := range []string{cwd, managed, tmp} {
		if e := os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	put := func(path, text string) {
		t.Helper()
		path = filepath.Join(home, path)
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
	}
	put("repo/.git/HEAD", "ref: refs/heads/main\n")
	for path, text := range map[string]string{
		".claude/skills/shared/SKILL.md":           "---\ndescription: user shared\n---\nuser",
		"repo/.agents/skills/shared/SKILL.md":      "---\ndescription: project agents shadow\n---\nproject",
		"repo/.claude/skills/shared/SKILL.md":      "---\ndescription: project native shadow\n---\nproject",
		"repo/.agents/skills/agents-only/SKILL.md": "---\ndescription: cross-client skill\n---\nbody",
		"repo/.claude/skills/false-desc/SKILL.md":  "---\ndescription: false\n---\nbody",
		"repo/.claude/skills/array-desc/SKILL.md":  "---\ndescription: [first, second]\n---\nbody",
		"repo/.claude/skills/hidden/SKILL.md":      "---\nuser-invocable: false\n---\nbody",
		"repo/.claude/skills/cost/SKILL.md":        "---\ndescription: disk shadows compiled cost\n---\nbody",
		"repo/.claude/commands/shared.md":          "---\ndescription: legacy must not shadow skill\n---\nbody",
		"repo/.claude/commands/nested/legacy.md":   "---\ndescription: actual old command\nargument-hint: '<required>'\n---\nbody $ARGUMENTS",
		".claude/commands/user.md":                 "---\ndescription: user legacy\n---\nbody",
		"repo/.claude/commands/disabled.md":        "---\nuser-invocable: false\n---\nbody",
		"plugin/skills/.visible/SKILL.md":          "---\ndescription: plugin hidden-directory skill\n---\nbody",
		"plugin/skills/visible/SKILL.md":           "---\ndescription: plugin original root wins\n---\nbody",
		"plugin/other/visible/SKILL.md":            "---\ndescription: duplicate plugin root loses\n---\nbody",
		"plugin/direct/SKILL.md":                   "---\ndescription: direct plugin\n---\nbody",
		"plugin/direct/nested/SKILL.md":            "---\ndescription: direct root child ignored\n---\nbody",
		"plugin/skills/disabled/SKILL.md":          "---\nuser-invocable: false\n---\nbody",
		".claude/skills/sample:visible/SKILL.md":   "---\ndescription: actual user skill overrides plugin\n---\nbody",
	} {
		put(path, text)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": tmp, "CC_HAHA_FIXTURE_TEMP_ROOT": filepath.Dir(home), "CC_HAHA_SLASH_COMMANDS_FIXTURE": "1", "NODE_ENV": "production", "BUN_OPTIONS": "--no-env-file", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "ANTHROPIC_BASE_URL": "http://127.0.0.1:9", "ANTHROPIC_API_KEY": "fake-offline-only", "PATH": "/usr/bin:/bin", "TZ": "UTC"}
	env["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	native, e := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: "darwin", ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	settings, e := clibootstrap.NewNativeProcessSettings(native, "darwin", clibootstrap.DefaultState(), storage.New(storage.Options{}))
	if e != nil {
		t.Fatal(e)
	}
	defer settings.Close(context.Background())
	plugins := []skillregistry.Plugin{{Name: "sample", SkillsPath: filepath.Join(home, "plugin", "skills"), SkillsPaths: []string{filepath.Join(home, "plugin", "other"), filepath.Join(home, "plugin", "direct")}}}
	sourcePlugins := []map[string]any{{"name": plugins[0].Name, "skillsPath": plugins[0].SkillsPath, "skillsPaths": plugins[0].SkillsPaths}}
	roots, e := skillregistry.New(skillregistry.Options{Home: home, ConfigDir: env["CLAUDE_CONFIG_DIR"], CWD: cwd, ProjectRoot: cwd, Environment: func(key string) string { return env[key] }, Settings: func(context.Context) (protocol.JSONObject, error) {
		value, e := settings.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), e
		}
		return value.Clone(), e
	}, Plugins: slashPluginFixture{plugins}})
	if e != nil {
		t.Fatal(e)
	}
	local, e := settings.SkillCommands(clibootstrap.NativeSkillOptions{})
	if e != nil {
		t.Fatal(e)
	}
	comparer := collate.New(language.English)
	var mu sync.Mutex
	compiledFailure := false
	warnings := []error{}
	service, e := slashcommandsapi.New(slashcommandsapi.Options{Skills: roots, LocalCommands: func(ctx context.Context, cwd string) ([]protocol.JSONObject, error) {
		definitions, e := local.Local(ctx, cwd)
		out := []protocol.JSONObject{}
		for _, definition := range definitions {
			out = append(out, definition.Metadata())
		}
		return out, e
	}, CompiledCommands: func(context.Context) ([]protocol.JSONObject, error) {
		if compiledFailure {
			return nil, errors.New("fixture compiled catalog failure")
		}
		bundled, e := skillcommands.NativeExternalBundled(skillcommands.BundledOptions{Environment: env})
		return sdkhost.CompiledHeadlessCommands(env, bundled), e
	}, Compare: func(a, b string) int { mu.Lock(); defer mu.Unlock(); return comparer.CompareString(a, b) }, Warn: func(e error) { warnings = append(warnings, e) }})
	if e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../server/testdata/slash_commands.fixture.ts")
	for _, include := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		input, _ := json.Marshal(map[string]any{"home": home, "cwd": cwd, "projectRoot": cwd, "list": true, "plugins": sourcePlugins, "includeCompiled": include})
		cmd.Stdin = bytes.NewReader(input)
		out, e := cmd.CombinedOutput()
		cancel()
		if e != nil {
			t.Fatalf("actual Source list %v %.4000s", e, out)
		}
		var source struct{ Commands []map[string]any }
		if e = json.Unmarshal(out, &source); e != nil {
			t.Fatalf("Source shape %v %.4000s", e, out)
		}
		commands, e := service.List(context.Background(), cwd, include)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(commands)
		var actual []map[string]any
		_ = json.Unmarshal(raw, &actual)
		if !reflect.DeepEqual(actual, source.Commands) {
			t.Errorf("compiled%v Go%s Source%s", include, raw, out)
		}
	}
	// The Source HTTP path owns normalization and merge. All fallback commands
	// still come from its actual files and compiled catalog; no merge helper is
	// copied into the comparator.
	preferredCases := []string{
		`[]`,
		`["shared","nested:legacy","wire-only"]`,
		`[{"name":"shared","description":"wire preferred","argumentHint":"<live>"}]`,
		`[{"name":"shared","description":"first"},{"name":"wire-only"},{"name":"shared","description":"last"}]`,
		`[null,false,0,[],{},"", "\ufeff \n",{"name":null,"command":"user","description":false},{"name":"","command":"ignored"}]`,
		`[{"command":"nested:legacy","argumentHint":""},{"name":"shared","description":[]} ,{"name":"array-desc","description":false}]`,
		`[{"name":" shared ","description":"whitespace identity"},"😀","\ud800","\udfff","\ufffd"]`,
		`[{"name":"wire-only","description":"\ud800","argumentHint":"\udfff"}]`,
		`[{"name":12,"command":"user","description":"source fallback"},{"name":false,"command":"shared"}]`,
	}
	scenarios := make([]map[string]any, 0, len(preferredCases))
	for _, preferred := range preferredCases {
		scenarios = append(scenarios, map[string]any{"preferred": json.RawMessage(preferred)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	input, _ := json.Marshal(map[string]any{"home": home, "cwd": cwd, "projectRoot": cwd, "merge": true, "plugins": sourcePlugins, "scenarios": scenarios})
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source HTTP merge %v %.6000s", e, out)
	}
	var source struct {
		Results []struct {
			Status int
			Body   struct{ Commands json.RawMessage }
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Results) != len(preferredCases) {
		t.Fatalf("Source merge shape %v %.6000s", e, out)
	}
	for index, preferred := range preferredCases {
		cache := server.NormalizeSessionSlashCommands(json.RawMessage(preferred))
		fallback, err := service.List(context.Background(), cwd, len(cache) == 0)
		if err != nil {
			t.Fatal(err)
		}
		commands := fallback
		if len(cache) > 0 {
			commands = slashcommandsapi.Merge(cache, fallback)
		}
		raw, _ := json.Marshal(commands)
		actual, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
		want, sourceErr := protocol.ParseAndStringifyJavaScriptJSON(source.Results[index].Body.Commands)
		if source.Results[index].Status != 200 || err != nil || sourceErr != nil || !bytes.Equal(actual, want) {
			t.Errorf("merge case%d status%d input%s Go%s Source%s errors%v/%v", index, source.Results[index].Status, preferred, actual, want, err, sourceErr)
		}
	}
	if len(warnings) != 0 {
		t.Fatal("healthy catalog warned", warnings)
	}
	compiledFailure = true
	input, _ = json.Marshal(map[string]any{"home": home, "cwd": cwd, "projectRoot": cwd, "list": true, "plugins": sourcePlugins, "compiledFailure": true, "includeCompiled": true})
	cmd = exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e = cmd.Output()
	if e != nil {
		t.Fatalf("Source compiled failure %v %.4000s", e, stderr.Bytes())
	}
	var failedSource struct{ Commands json.RawMessage }
	if e = json.Unmarshal(out, &failedSource); e != nil {
		t.Fatal(e, string(out))
	}
	commands, err := service.List(context.Background(), cwd, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(commands)
	actual, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
	want, _ := protocol.ParseAndStringifyJavaScriptJSON(failedSource.Commands)
	if !bytes.Equal(actual, want) || len(warnings) != 1 || !bytes.Contains(stderr.Bytes(), []byte("fixture compiled catalog failure")) {
		t.Fatalf("compiled failure was hidden or did not preserve actual disk menu Go%s Source%s warnings%v stderr%s", actual, want, warnings, stderr.Bytes())
	}
	canceled, cancelList := context.WithCancel(context.Background())
	cancelList()
	if _, err = service.List(canceled, cwd, true); !errors.Is(err, context.Canceled) || len(warnings) != 1 {
		t.Fatal("canceled lookup called catalog or returned success", err, warnings)
	}
}

// Only the external enabled-plugin producer is controlled. Both runtimes
// discover, parse, merge and filter the actual plugin directories themselves.
type slashPluginFixture struct{ values []skillregistry.Plugin }

func (p slashPluginFixture) EnabledForSkills(context.Context) ([]skillregistry.Plugin, error) {
	return p.values, nil
}
