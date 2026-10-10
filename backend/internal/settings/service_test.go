package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

var testContext = context.Background()

func object(raw string) storage.Object {
	value, valid, err := storage.NormalizeObject(json.RawMessage(raw))
	if err != nil || !valid {
		panic("invalid test object: " + raw)
	}
	return value
}

func assertObject(t *testing.T, actual storage.Object, expected string) {
	t.Helper()
	var actualValue, expectedValue any
	raw, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &actualValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &expectedValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualValue, expectedValue) {
		t.Fatalf("settings = %s, want %s", raw, expected)
	}
}

func fixtureOptions(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, "config")}
	return Options{
		Store: storage.New(storage.Options{Warn: func(string) {}}), HomeDir: home,
		Environment: func() map[string]string { return env },
		// Explicit fixture bootstrap: these unit tests inject migration behavior
		// below. This callback is never installed in a production entrypoint.
		EnsureMigrated:   func(context.Context, string) error { return nil },
		ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil },
		GitIgnore:        func(context.Context, string, string) error { return nil },
	}
}

func mustService(t *testing.T, options Options) *Service {
	t.Helper()
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func seed(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}

func fileObject(t *testing.T, path string) storage.Object {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return object(string(raw))
}

func TestBootstrapRequiresActualMigrationAndExplicitEnvironmentDependencies(t *testing.T) {
	for _, missing := range []string{"store", "home", "environment", "migration", "shell", "git"} {
		t.Run(missing, func(t *testing.T) {
			options := fixtureOptions(t)
			switch missing {
			case "store":
				options.Store = nil
			case "home":
				options.HomeDir = ""
			case "environment":
				options.Environment = nil
			case "migration":
				options.EnsureMigrated = nil
			case "shell":
				options.ShellEnvironment = nil
			case "git":
				options.GitIgnore = nil
			}
			if _, err := New(options); err == nil {
				t.Fatal("missing bootstrap dependency accepted")
			}
		})
	}
	options := fixtureOptions(t)
	options.GitIgnore = nil
	options.GitExecutable = filepath.Join(options.HomeDir, "fixture-git")
	if _, err := New(options); err != nil {
		t.Fatalf("concrete injected git adapter rejected: %v", err)
	}
}

func TestSelectedConfigPathsAndPerReadMigrationCallbacks(t *testing.T) {
	options := fixtureOptions(t)
	var migrated []string
	options.EnsureMigrated = func(_ context.Context, config string) error { migrated = append(migrated, config); return nil }
	service := mustService(t, options)
	value, err := service.GetUserSettings(testContext)
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, value, `{}`)
	env := options.Environment()
	selected := env["CLAUDE_CONFIG_DIR"]
	if len(migrated) != 1 || migrated[0] != selected {
		t.Fatalf("migration source = %v", migrated)
	}
	if _, err := os.Stat(filepath.Join(selected, "settings.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("read created a settings file")
	}
	if err := service.UpdateUserSettings(testContext, object(`{"theme":"white"}`)); err != nil {
		t.Fatal(err)
	}
	assertObject(t, fileObject(t, filepath.Join(selected, "settings.json")), `{"theme":"white"}`)
	env["CLAUDE_CONFIG_DIR"] = ""
	if err := service.UpdateUserSettings(testContext, object(`{"theme":"dark"}`)); err != nil {
		t.Fatal(err)
	}
	assertObject(t, fileObject(t, filepath.Join(options.HomeDir, ".claude", "settings.json")), `{"theme":"dark"}`)
	options.ConfigDir = filepath.Join(options.HomeDir, "explicit")
	service = mustService(t, options)
	if err := service.UpdateUserSettings(testContext, object(`{"theme":"light"}`)); err != nil {
		t.Fatal(err)
	}
	assertObject(t, fileObject(t, filepath.Join(options.ConfigDir, "settings.json")), `{"theme":"light"}`)
}

func TestUserPatchShallowMergeSpecialTerminalMergeAndUndefinedDeletion(t *testing.T) {
	options := fixtureOptions(t)
	var invalidations int
	options.InvalidateCache = func() { invalidations++ }
	service := mustService(t, options)
	path := filepath.Join(service.configDir(), "settings.json")
	old, err := os.ReadFile("testdata/legacy-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	seed(t, path, string(old))
	patch := object(`{"theme":"white","env":{"REPLACEMENT":"value"},"desktopTerminal":{"startupShell":"pwsh","customShellPath":"C:\\Program Files\\PowerShell\\7\\pwsh.exe"}}`)
	patch["removeMe"] = nil
	if err := service.UpdateUserSettings(testContext, patch); err != nil {
		t.Fatal(err)
	}
	value, err := service.GetUserSettings(testContext)
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, value, `{"theme":"white","env":{"REPLACEMENT":"value"},"desktopTerminal":{"startupShell":"pwsh","customShellPath":"C:\\Program Files\\PowerShell\\7\\pwsh.exe","legacyEncoding":"utf-8","futureProfile":{"id":"work","inheritEnvironment":false}},"futureSetting":{"keep":true}}`)
	if invalidations != 1 {
		t.Fatalf("cache invalidation = %d", invalidations)
	}
	if err := service.UpdateUserSettings(testContext, object(`{"desktopTerminal":null,"futureSetting":null}`)); err != nil {
		t.Fatal(err)
	}
	if string(fileObject(t, path)["desktopTerminal"]) != "null" {
		t.Fatal("JSON null incorrectly merged or deleted")
	}
}

func TestAgentTeamsReadTimeLegacyPrecedenceAndNoWrites(t *testing.T) {
	for _, fixture := range []struct {
		name, user, managed, inherited string
		want                           bool
		shellCalls                     int
	}{
		{"default", `{}`, `{}`, "", true, 1},
		{"explicit true", `{"agentTeamsEnabled":true,"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"0"}}`, `{}`, "0", true, 0},
		{"explicit false", `{"agentTeamsEnabled":false}`, `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"1"}}`, "1", false, 0},
		{"managed wins", `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"1"}}`, `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"0"}}`, "1", false, 1},
		{"user before shell", `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"1"}}`, `{}`, "0", true, 1},
		{"shell opt out", `{}`, `{}`, "0", false, 1},
		{"managed null falls through", `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"YES"}}`, `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":null}}`, "0", true, 1},
		{"managed empty stops fallback", `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"1"}}`, `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":""}}`, "1", false, 1},
		{"managed nonstring stops fallback", `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"0"}}`, `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":false}}`, "0", true, 1},
		{"malformed env object ignored", `{"agentTeamsEnabled":"false","env":[]}`, `{"env":true}`, " on ", true, 1},
		{"JS trim BOM", `{}`, `{}`, "\ufeffTRUE\ufeff", true, 1},
		{"U0085 not JS whitespace", `{}`, `{}`, "\u0085TRUE\u0085", false, 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			options := fixtureOptions(t)
			calls := 0
			options.ShellEnvironment = func(context.Context) (map[string]string, error) {
				calls++
				if fixture.name == "default" {
					return map[string]string{}, nil
				}
				return map[string]string{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": fixture.inherited}, nil
			}
			service := mustService(t, options)
			userPath := filepath.Join(service.configDir(), "settings.json")
			managedPath := filepath.Join(service.configDir(), "cc-haha", "settings.json")
			seed(t, userPath, fixture.user)
			seed(t, managedPath, fixture.managed)
			enabled, err := service.GetAgentTeamsEnabled(testContext)
			if err != nil || enabled != fixture.want || calls != fixture.shellCalls {
				t.Fatalf("teams = %v, %v; shell calls %d", enabled, err, calls)
			}
			userAfter, _ := os.ReadFile(userPath)
			managedAfter, _ := os.ReadFile(managedPath)
			if string(userAfter) != fixture.user || string(managedAfter) != fixture.managed {
				t.Fatal("legacy preference was persisted on read")
			}
		})
	}
}

func TestAgentTeamsValidationAndDependencyErrors(t *testing.T) {
	options := fixtureOptions(t)
	service := mustService(t, options)
	for _, value := range []string{`null`, `"true"`, `0`, `{}`, `[]`} {
		err := service.UpdateUserSettings(testContext, storage.Object{"agentTeamsEnabled": json.RawMessage(value)})
		var apiError *APIError
		if !errors.As(err, &apiError) || apiError.StatusCode != 400 || apiError.Code != "BAD_REQUEST" || apiError.Message != "agentTeamsEnabled must be a boolean" {
			t.Fatalf("invalid teams flag accepted: %s, %v", value, err)
		}
	}
	failure := errors.New("shell fixture failed")
	options.ShellEnvironment = func(context.Context) (map[string]string, error) { return nil, failure }
	service = mustService(t, options)
	seed(t, filepath.Join(service.configDir(), "cc-haha", "settings.json"), `{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"1"}}`)
	if _, err := service.GetAgentTeamsEnabled(testContext); !errors.Is(err, failure) {
		t.Fatalf("shell failure hidden behind managed value: %v", err)
	}
}

func TestProjectLocalScopesOverrideAndUserProjectOnlyMergedSettings(t *testing.T) {
	options := fixtureOptions(t)
	options.ProjectRoot = filepath.Join(options.HomeDir, "default-project")
	var gitRule, gitRoot string
	options.GitIgnore = func(_ context.Context, rule, root string) error { gitRule, gitRoot = rule, root; return nil }
	service := mustService(t, options)
	if err := service.UpdateUserSettings(testContext, object(`{"theme":"dark","env":{"user":"keep"},"model":"opus"}`)); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateProjectSettings(testContext, object(`{"theme":"light","env":{"project":"replace"}}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateLocalSettings(testContext, object(`{"theme":"white","outputStyle":"Learning"}`), ""); err != nil {
		t.Fatal(err)
	}
	service.DrainBackground()
	if gitRule != ".claude/settings.local.json" || gitRoot != options.ProjectRoot {
		t.Fatalf("wrong gitignore request: %s, %s", gitRule, gitRoot)
	}
	merged, err := service.GetSettings(testContext, "")
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, merged, `{"theme":"light","env":{"project":"replace"},"model":"opus"}`)
	local, err := service.GetLocalSettings(testContext, "")
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, local, `{"theme":"white","outputStyle":"Learning"}`)
	explicit := filepath.Join(options.HomeDir, "other-project")
	if err := service.UpdateProjectSettings(testContext, object(`{"theme":"other"}`), explicit); err != nil {
		t.Fatal(err)
	}
	project, err := service.GetProjectSettings(testContext, explicit)
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, project, `{"theme":"other"}`)
	if _, err := os.Stat(filepath.Join(explicit, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
}

func TestMissingProjectRootErrorsAndMergedUserFallback(t *testing.T) {
	service := mustService(t, fixtureOptions(t))
	if err := service.UpdateUserSettings(testContext, object(`{"theme":"dark"}`)); err != nil {
		t.Fatal(err)
	}
	for _, action := range []struct {
		scope string
		call  func() error
	}{
		{"project", func() error { _, err := service.GetProjectSettings(testContext, ""); return err }},
		{"local", func() error { _, err := service.GetLocalSettings(testContext, ""); return err }},
		{"project", func() error { return service.UpdateProjectSettings(testContext, object(`{}`), "") }},
		{"local", func() error { return service.UpdateLocalSettings(testContext, object(`{}`), "") }},
	} {
		var apiError *APIError
		if err := action.call(); !errors.As(err, &apiError) || apiError.StatusCode != 400 || err.Error() != "Project root is required for "+action.scope+" settings" {
			t.Fatalf("missing project root: %v", err)
		}
	}
	value, err := service.GetSettings(testContext, "")
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, value, `{"theme":"dark"}`)
}

func TestOfficialMappingPreservesUnrelatedEnvDeletesFableAndModelContext(t *testing.T) {
	service := mustService(t, fixtureOptions(t))
	path := filepath.Join(service.configDir(), "settings.json")
	seed(t, path, `{"future":{"keep":true},"modelContext":200000,"env":{"USER_ENV":"keep","ANTHROPIC_DEFAULT_FABLE_MODEL":"old","futureEnv":{"keep":true}}}`)
	models := ModelMapping{Main: "main", Fable: "fable", Haiku: "haiku", Sonnet: "sonnet", Opus: "opus"}
	if err := service.UpdateOfficialModelMapping(testContext, models); err != nil {
		t.Fatal(err)
	}
	assertObject(t, fileObject(t, path), `{"future":{"keep":true},"model":"main","env":{"USER_ENV":"keep","futureEnv":{"keep":true},"ANTHROPIC_MODEL":"main","ANTHROPIC_DEFAULT_FABLE_MODEL":"fable","ANTHROPIC_DEFAULT_HAIKU_MODEL":"haiku","ANTHROPIC_DEFAULT_SONNET_MODEL":"sonnet","ANTHROPIC_DEFAULT_OPUS_MODEL":"opus"}}`)
	models.Fable = ""
	if err := service.UpdateOfficialModelMapping(testContext, models); err != nil {
		t.Fatal(err)
	}
	if _, exists := objectValue(fileObject(t, path)["env"])["ANTHROPIC_DEFAULT_FABLE_MODEL"]; exists {
		t.Fatal("stale Fable alias remains")
	}
}

func TestBuiltInOverridesOwnFieldUnsetResetAndUnknownPreservation(t *testing.T) {
	service := mustService(t, fixtureOptions(t))
	path := filepath.Join(service.configDir(), "settings.json")
	seed(t, path, `{"future":{"keep":true},"builtInAgentOverrides":{"Plan":{"model":"opus","futurePlan":"keep"},"Explore":{"model":"sonnet","effort":"high","futureAgent":{"keep":true}}}}`)
	if err := service.UpdateBuiltInAgentOverride(testContext, "Explore", object(`{"model":null,"ignoredFuturePatch":"ignored"}`)); err != nil {
		t.Fatal(err)
	}
	assertObject(t, fileObject(t, path), `{"future":{"keep":true},"builtInAgentOverrides":{"Plan":{"model":"opus","futurePlan":"keep"},"Explore":{"effort":"high","futureAgent":{"keep":true}}}}`)
	if err := service.UpdateBuiltInAgentOverride(testContext, "Explore", nil); err != nil {
		t.Fatal(err)
	}
	assertObject(t, fileObject(t, path), `{"future":{"keep":true},"builtInAgentOverrides":{"Plan":{"model":"opus","futurePlan":"keep"},"Explore":{"futureAgent":{"keep":true}}}}`)
	if err := service.UpdateBuiltInAgentOverride(testContext, "General", object(`{"model":"haiku","effort":7}`)); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateBuiltInAgentOverride(testContext, "General", object(`{"model":null,"effort":null}`)); err != nil {
		t.Fatal(err)
	}
	if _, exists := objectValue(fileObject(t, path)["builtInAgentOverrides"])["General"]; exists {
		t.Fatal("empty override entry remains")
	}
	seed(t, path, `{"builtInAgentOverrides":{"Explore":{"model":"haiku"}}}`)
	if err := service.UpdateBuiltInAgentOverride(testContext, "Explore", nil); err != nil {
		t.Fatal(err)
	}
	assertObject(t, fileObject(t, path), `{}`)
	if err := service.UpdateBuiltInAgentOverride(testContext, "Explore", nil); err != nil {
		t.Fatal("absent reset must succeed", err)
	}
	seed(t, path, `{"builtInAgentOverrides":["legacy"]}`)
	if err := service.UpdateBuiltInAgentOverride(testContext, "Plan", object(`{"effort":"low"}`)); err != nil {
		t.Fatal(err)
	}
	assertObject(t, fileObject(t, path), `{"builtInAgentOverrides":{"Plan":{"effort":"low"}}}`)
}

func TestPermissionModesExactValidationAndInvalidLegacyFallback(t *testing.T) {
	service := mustService(t, fixtureOptions(t))
	for _, mode := range ValidPermissionModes() {
		if err := service.SetPermissionMode(testContext, mode); err != nil {
			t.Fatal(err)
		}
		actual, err := service.GetPermissionMode(testContext)
		if err != nil || actual != mode {
			t.Fatalf("permission mode %s = %s, %v", mode, actual, err)
		}
	}
	for _, invalid := range []string{`"legacy-yolo"`, `null`, `42`, `[]`} {
		seed(t, filepath.Join(service.configDir(), "settings.json"), `{"defaultMode":`+invalid+`}`)
		actual, err := service.GetPermissionMode(testContext)
		if err != nil || actual != "default" {
			t.Fatalf("legacy mode not defaulted: %s, %v", actual, err)
		}
	}
	var apiError *APIError
	err := service.SetPermissionMode(testContext, "invalid")
	if !errors.As(err, &apiError) || apiError.Code != "BAD_REQUEST" || apiError.StatusCode != 400 || apiError.Message != `Invalid permission mode: "invalid". Valid modes: default, acceptEdits, plan, bypassPermissions, dontAsk, auto` {
		t.Fatalf("permission validation contract changed: %v", err)
	}
	modes := ValidPermissionModes()
	modes[0] = "mutated"
	if !IsValidPermissionMode("default") {
		t.Fatal("caller mutated validation list")
	}
}

func TestSharedStoreSerializesUpdatesAcrossServiceInstances(t *testing.T) {
	options := fixtureOptions(t)
	var invalidations atomic.Int64
	options.InvalidateCache = func() { invalidations.Add(1) }
	first, second := mustService(t, options), mustService(t, options)
	var group sync.WaitGroup
	failures := make(chan error, 40)
	for index := 0; index < 20; index++ {
		group.Add(2)
		go func(index int) {
			defer group.Done()
			failures <- first.UpdateUserSettings(testContext, object(fmt.Sprintf(`{"field-%d":true}`, index)))
		}(index)
		go func(index int) {
			defer group.Done()
			failures <- second.UpdateBuiltInAgentOverride(testContext, fmt.Sprintf("agent-%d", index), object(`{"model":"sonnet"}`))
		}(index)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	value, err := first.GetUserSettings(testContext)
	if err != nil || len(value) != 21 || len(objectValue(value["builtInAgentOverrides"])) != 20 || invalidations.Load() != 40 {
		t.Fatalf("cross-instance update lost: %d, %v, %d", len(value), err, invalidations.Load())
	}
}

func TestMigrationFailureStopsReadsAndWritesWithoutFalseCompletion(t *testing.T) {
	options := fixtureOptions(t)
	failure := errors.New("fixture migration is unavailable")
	options.EnsureMigrated = func(context.Context, string) error { return failure }
	service := mustService(t, options)
	for _, call := range []func() error{
		func() error { _, err := service.GetUserSettings(testContext); return err },
		func() error { _, err := service.GetAgentTeamsEnabled(testContext); return err },
		func() error { _, err := service.GetPermissionMode(testContext); return err },
		func() error { _, err := service.GetSettings(testContext, ""); return err },
		func() error { return service.UpdateUserSettings(testContext, object(`{"theme":"dark"}`)) },
		func() error { return service.UpdateOfficialModelMapping(testContext, ModelMapping{}) },
		func() error { return service.UpdateBuiltInAgentOverride(testContext, "Plan", nil) },
		func() error {
			return service.UpdateProjectSettings(testContext, object(`{}`), filepath.Join(options.HomeDir, "project"))
		},
		func() error {
			return service.UpdateLocalSettings(testContext, object(`{}`), filepath.Join(options.HomeDir, "project"))
		},
	} {
		if err := call(); !errors.Is(err, failure) {
			t.Fatalf("migration error hidden: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(service.configDir(), "settings.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("failed migration created settings")
	}
}

type failingFS struct {
	readFailure  string
	writeFailure bool
	readCalls    atomic.Int64
}

func (filesystem *failingFS) ReadFile(path string) ([]byte, error) {
	filesystem.readCalls.Add(1)
	if path == filesystem.readFailure {
		return nil, fs.ErrPermission
	}
	return os.ReadFile(path)
}
func (filesystem *failingFS) WriteFile(path string, value []byte, mode fs.FileMode) error {
	if filesystem.writeFailure {
		return fs.ErrPermission
	}
	return os.WriteFile(path, value, mode)
}
func (*failingFS) MkdirAll(path string, mode fs.FileMode) error { return os.MkdirAll(path, mode) }
func (*failingFS) Rename(old, next string) error                { return os.Rename(old, next) }
func (*failingFS) Remove(path string) error                     { return os.Remove(path) }
func (*failingFS) CopyFile(old, next string) error {
	raw, err := os.ReadFile(old)
	if err != nil {
		return err
	}
	return os.WriteFile(next, raw, 0600)
}

func TestIOFailuresKeepCommittedSettingsAndDoNotInvalidateCache(t *testing.T) {
	options := fixtureOptions(t)
	filesystem := &failingFS{}
	options.Store = storage.New(storage.Options{FS: filesystem, Warn: func(string) {}})
	invalidations := 0
	options.InvalidateCache = func() { invalidations++ }
	service := mustService(t, options)
	user := filepath.Join(service.configDir(), "settings.json")
	seed(t, user, `{"committed":true}`)
	filesystem.writeFailure = true
	if err := service.UpdateUserSettings(testContext, object(`{"new":true}`)); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("write failure hidden: %v", err)
	}
	assertObject(t, fileObject(t, user), `{"committed":true}`)
	if invalidations != 0 {
		t.Fatal("failed write invalidated cache")
	}
	filesystem.writeFailure = false
	filesystem.readFailure = user
	if _, err := service.GetUserSettings(testContext); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("user read failure hidden: %v", err)
	}
	if _, err := service.GetAgentTeamsEnabled(testContext); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("teams user read failure hidden: %v", err)
	}
	filesystem.readFailure = filepath.Join(service.configDir(), "cc-haha", "settings.json")
	if _, err := service.GetAgentTeamsEnabled(testContext); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("teams managed read failure hidden: %v", err)
	}
	project := filepath.Join(options.HomeDir, "project")
	filesystem.readFailure = filepath.Join(project, ".claude", "settings.json")
	merged, err := service.GetSettings(testContext, project)
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, merged, `{"committed":true}`)
}

func TestLocalGitignoreFailureIsLoggedAfterSuccessfulSave(t *testing.T) {
	options := fixtureOptions(t)
	options.ProjectRoot = filepath.Join(options.HomeDir, "project")
	failure := errors.New("gitignore fixture failed")
	options.GitIgnore = func(context.Context, string, string) error { return failure }
	var logged error
	options.LogError = func(err error) { logged = err }
	service := mustService(t, options)
	if err := service.UpdateLocalSettings(testContext, object(`{"outputStyle":"Learning"}`), ""); err != nil {
		t.Fatal(err)
	}
	service.DrainBackground()
	if !errors.Is(logged, failure) {
		t.Fatalf("gitignore failure not logged: %v", logged)
	}
	value, err := service.GetLocalSettings(testContext, "")
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, value, `{"outputStyle":"Learning"}`)
}

func TestMalformedSettingsRecoverAndReadOnlyDefaultLeavesBlankUntouched(t *testing.T) {
	service := mustService(t, fixtureOptions(t))
	path := filepath.Join(service.configDir(), "settings.json")
	seed(t, path, `{not JSON`)
	value, err := service.GetUserSettings(testContext)
	if err != nil {
		t.Fatal(err)
	}
	assertObject(t, value, `{}`)
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 || !strings.HasPrefix(files[0].Name(), "settings.json.invalid-") {
		t.Fatalf("corrupt source not preserved: %v", files)
	}
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(path), files[0].Name()))
	if string(raw) != `{not JSON` {
		t.Fatal("quarantine bytes changed")
	}
	seed(t, path, " \n\t")
	if _, err := service.GetUserSettings(testContext); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if !bytes.Equal(raw, []byte(" \n\t")) {
		t.Fatal("blank source was rewritten")
	}
}

func TestInvalidRawPatchesNeverWriteOrLoseCurrentFields(t *testing.T) {
	service := mustService(t, fixtureOptions(t))
	patch := storage.Object{"broken": json.RawMessage("{")}
	for _, call := range []func() error{
		func() error { return service.UpdateUserSettings(testContext, patch) },
		func() error { return service.UpdateProjectSettings(testContext, patch, "project") },
		func() error { return service.UpdateLocalSettings(testContext, patch, "project") },
		func() error { return service.UpdateBuiltInAgentOverride(testContext, "Plan", patch) },
	} {
		if err := call(); err == nil {
			t.Fatal("invalid raw patch accepted")
		}
	}
}
