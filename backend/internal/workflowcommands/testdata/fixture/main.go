package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

func quote(s string) json.RawMessage { raw, _ := json.Marshal(s); return raw }
func object(raw []byte) protocol.JSONObject {
	var o protocol.JSONObject
	if e := o.UnmarshalJSON(raw); e != nil {
		panic(e)
	}
	return o
}
func canonical(p string) string {
	s, e := filepath.EvalSymlinks(p)
	if e != nil {
		panic(e)
	}
	return s
}
func main() {
	if e := execute(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func execute() error {
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) || canonical(home) != home || os.Getenv("USERPROFILE") != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" || os.Getenv("CC_HAHA_WORKFLOW_FIXTURE") != "1" || os.Getenv("NODE_ENV") != "production" || os.Getenv("TZ") != "UTC" {
		return errors.New("private original workflow environment required")
	}
	allocation := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if allocation == "" || !strings.HasPrefix(home, canonical(allocation)+string(filepath.Separator)) {
		return errors.New("trusted allocation required")
	}
	cwd, e := os.Getwd()
	if e != nil || canonical(cwd) != home {
		return errors.New("private cwd required")
	}
	if info, e := os.Lstat(filepath.Join(home, ".claude")); e != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return errors.New("private config directory required")
	}
	for _, leaf := range []string{"input.json", "result.json"} {
		info, e := os.Lstat(filepath.Join(home, leaf))
		if e == nil && (!info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0) {
			return errors.New("private input/output leaf required")
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	inputRaw, e := os.ReadFile(filepath.Join(home, "input.json"))
	if e != nil {
		return e
	}
	input := object(inputRaw)
	for _, p := range input.OwnProperties() {
		var key string
		_ = json.Unmarshal(p.NameJSON, &key)
		switch key {
		case "id", "files", "settings", "environment", "inlinePlugins", "operations":
		default:
			return errors.New("unknown original program field")
		}
	}
	var id string
	_ = json.Unmarshal(input.Get("id"), &id)
	var files, operations, plugins []json.RawMessage
	_ = json.Unmarshal(input.Get("files"), &files)
	_ = json.Unmarshal(input.Get("operations"), &operations)
	_ = json.Unmarshal(input.Get("inlinePlugins"), &plugins)
	if id == "" || len(files) > 40 || len(operations) > 32 {
		return errors.New("bounded original program required")
	}
	privatePath := func(raw json.RawMessage) (string, error) {
		var p string
		if e := json.Unmarshal(raw, &p); e != nil || p == "" || p == "input.json" || p == "result.json" || p == "production-imported.json" || filepath.IsAbs(p) || strings.ContainsAny(p, "\\\x00") {
			return "", errors.New("private relative path required")
		}
		for _, part := range strings.Split(p, "/") {
			if part == "" || part == "." || part == ".." {
				return "", errors.New("private relative segments required")
			}
		}
		path := filepath.Join(home, p)
		ancestor := path
		for {
			_, e := os.Lstat(ancestor)
			if e == nil {
				resolved, e := filepath.EvalSymlinks(ancestor)
				if e != nil || resolved != home && !strings.HasPrefix(resolved, home+string(filepath.Separator)) {
					return "", errors.New("private path canonical escape")
				}
				break
			}
			if !os.IsNotExist(e) {
				return "", e
			}
			ancestor = filepath.Dir(ancestor)
		}
		return path, nil
	}
	// Preflight all original control and capabilities before fixture writes.
	for _, property := range object(input.Get("environment")).OwnProperties() {
		if string(property.NameJSON) != `"CLAUDE_CODE_DISABLE_WORKFLOWS"` {
			return errors.New("original workflow fixture environment boundary")
		}
		if _, e := protocol.DecodeJavaScriptJSONString(property.Value); e != nil {
			return errors.New("original environment string required")
		}
	}
	for _, raw := range files {
		entry := object(raw)
		for _, p := range entry.OwnProperties() {
			var k string
			_ = json.Unmarshal(p.NameJSON, &k)
			switch k {
			case "path", "text", "recipe", "directory", "privateSymlinkTarget":
			default:
				return errors.New("unknown original file control")
			}
		}
		if _, e := privatePath(entry.Get("path")); e != nil {
			return e
		}
		if target := entry.Get("privateSymlinkTarget"); target != nil {
			if _, e := privatePath(target); e != nil {
				return e
			}
		}
	}
	for _, raw := range operations {
		operation := object(raw)
		var op string
		_ = json.Unmarshal(operation.Get("op"), &op)
		for _, p := range operation.OwnProperties() {
			var k string
			_ = json.Unmarshal(p.NameJSON, &k)
			switch k {
			case "op", "entry", "label", "args", "path", "text", "value":
			default:
				return errors.New("unknown original operation control")
			}
		}
		switch op {
		case "list":
			var entry string
			_ = json.Unmarshal(operation.Get("entry"), &entry)
			if entry != "commands" && entry != "factory" && entry != "discovery" {
				return errors.New("unknown original list entry")
			}
			var args []json.RawMessage
			_ = json.Unmarshal(operation.Get("args"), &args)
			for _, arg := range args {
				if _, e := protocol.DecodeJavaScriptJSONString(arg); e != nil {
					return errors.New("original prompt string required")
				}
			}
		case "resetSettingsCache", "clearCommandMemoizationCaches", "pluginCache":
		case "setEnv":
			if _, e := protocol.DecodeJavaScriptJSONString(operation.Get("value")); e != nil {
				return errors.New("original live env string required")
			}
		case "writeFile", "removeFile":
			if _, e := privatePath(operation.Get("path")); e != nil {
				return e
			}
		default:
			return errors.New("unknown original operation")
		}
	}
	settings := object(input.Get("settings"))
	environment := object(input.Get("environment"))
	trace := []any{}
	snapshots := []any{}
	diagnostics := []string{}
	result := map[string]any{"original": input, "home": home, "actualPid": os.Getpid(), "trace": trace, "snapshots": snapshots, "errors": []any{}, "diagnostics": diagnostics, "scope": "actual original Workflow component; full registry and CLI not migrated"}
	finish := func(failure error) error {
		result["trace"] = trace
		result["snapshots"] = snapshots
		result["diagnostics"] = diagnostics
		if failure != nil {
			result["failure"] = map[string]any{"name": fmt.Sprintf("%T", failure), "message": failure.Error()}
		}
		raw, e := json.Marshal(result)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(home, "result.json"), raw, 0600); e != nil {
			return e
		}
		fmt.Println(string(raw))
		return nil
	}
	if settings.Get("managed") != nil {
		return finish(&workflowcommands.CapabilityError{Boundary: "active managed policy files not migrated by existing absent-policy CLI resolver"})
	}
	if len(plugins) > 0 {
		return finish(&workflowcommands.CapabilityError{Boundary: "plugin manager side effects not migrated; original Source namespace absence retained"})
	}
	for _, dir := range []string{filepath.Join(home, "repo", "nested"), filepath.Join(home, "repo", ".git")} {
		if e = os.MkdirAll(dir, 0777); e != nil {
			return e
		}
	}
	for _, p := range settings.OwnProperties() {
		var source string
		_ = json.Unmarshal(p.NameJSON, &source)
		path := ""
		switch source {
		case "user":
			path = filepath.Join(home, ".claude", "settings.json")
		case "project":
			path = filepath.Join(home, "repo", ".claude", "settings.json")
		default:
			return errors.New("unknown settings source")
		}
		if e = os.MkdirAll(filepath.Dir(path), 0777); e != nil {
			return e
		}
		if e = os.WriteFile(path, p.Value, 0666); e != nil {
			return e
		}
	}
	for _, raw := range files {
		file := object(raw)
		path, e := privatePath(file.Get("path"))
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(path), 0777); e != nil {
			return e
		}
		if file.Get("directory") != nil {
			e = os.MkdirAll(path, 0777)
		} else if file.Get("privateSymlinkTarget") != nil {
			target, e := privatePath(file.Get("privateSymlinkTarget"))
			if e != nil {
				return e
			}
			e = os.Symlink(target, path)
			if e != nil {
				return e
			}
		} else {
			var text string
			if file.Get("text") != nil {
				_ = json.Unmarshal(file.Get("text"), &text)
			} else {
				recipe := object(file.Get("recipe"))
				var prefix, pattern string
				var repeat int
				_ = json.Unmarshal(recipe.Get("prefix"), &prefix)
				_ = json.Unmarshal(recipe.Get("pattern"), &pattern)
				_ = json.Unmarshal(recipe.Get("repeat"), &repeat)
				if repeat < 0 || repeat > 600000 {
					return errors.New("bounded original recipe required")
				}
				text = prefix + strings.Repeat(pattern, repeat)
			}
			e = os.WriteFile(path, []byte(text), 0666)
		}
		if e != nil {
			return e
		}
	}
	owner := cooperative.New()
	defer owner.Close()
	config := filepath.Join(home, ".claude")
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: time.Now, PID: os.Getpid()})
	if e != nil {
		return e
	}
	defer global.Close()
	if e = global.Enable(); e != nil {
		return e
	}
	cache, _ := clisettings.NewState(func() float64 { return float64(time.Now().UnixMilli()) })
	settingsService, e := clisettings.NewService(clisettings.Options{State: cache, ConfigHome: config, OriginalCWD: filepath.Join(home, "repo", "nested"), Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return float64(time.Now().UnixMilli()) }, PID: os.Getpid()})
	if e != nil {
		return e
	}
	envObject := protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(config)}, protocol.JSONField{Name: "USER_TYPE", Value: quote("external")}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")})
	envObject.Assign(environment)
	env, e := clistartupenv.NewEnvironment(envObject)
	if e != nil {
		return e
	}
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settingsService, SettingsState: cache, Environment: env, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if e != nil {
		return e
	}
	gate, _ := workflowcommands.NewGate(owner, startup, env)
	state, _ := bootstrapstate.New(owner, "00000000-0000-4000-8000-000000000001", filepath.Join(home, "repo", "nested"), func() (string, error) { return "", errors.New("Workflow component must not allocate a UUID") })
	resolver, _ := bootstrapstate.NewConfigResolver(owner, bootstrapstate.ConfigHost{Environment: func(key string) (string, bool) {
		if key == "CLAUDE_CONFIG_DIR" {
			return config, true
		}
		return "", false
	}, HomeDir: func() (string, error) { return home, nil }})
	git, e := gitbranch.New(gitbranch.Options{Owner: owner, CWD: func() string { return state.Snapshot().OriginalCWD }, Now: func() int64 { return time.Now().UnixMilli() }, StatSync: os.Stat, Stat: os.Stat, ReadFile: os.ReadFile, WatchFile: (&gitbranch.NativePollWatch{Stat: os.Stat}).Watch, ScrollDraining: func() bool { return false }, RegisterCleanup: func(func(*cooperative.Scope) error) {
		panic("FindRoot-only Workflow component must not initialize branch watchers")
	}})
	if e != nil {
		return e
	}
	var service *workflowcommands.Service
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := state.SetProjectRoot(scope, filepath.Join(home, "repo")); e != nil {
			return e
		}
		var e error
		service, e = workflowcommands.New(scope, workflowcommands.Options{Owner: owner, State: state, Config: resolver, Gate: gate, Git: git, Home: home, Diagnostic: func(_ *cooperative.Scope, message string) { diagnostics = append(diagnostics, message) }})
		return e
	})
	if e != nil {
		return e
	}
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, raw := range operations {
			operation := object(raw)
			var op string
			_ = json.Unmarshal(operation.Get("op"), &op)
			trace = append(trace, map[string]any{"event": "operation:start", "original": operation})
			switch op {
			case "resetSettingsCache":
				cache.ResetSettingsCache()
			case "clearCommandMemoizationCaches":
				if e = service.ClearCommandCache(scope); e != nil {
					return e
				}
			case "setEnv":
				if e = env.SetProperty(quote("CLAUDE_CODE_DISABLE_WORKFLOWS"), operation.Get("value")); e != nil {
					return e
				}
			case "writeFile", "removeFile":
				path, err := privatePath(operation.Get("path"))
				if err != nil {
					return err
				}
				if op == "removeFile" {
					e = os.Remove(path)
				} else {
					var text string
					_ = json.Unmarshal(operation.Get("text"), &text)
					if e = os.MkdirAll(filepath.Dir(path), 0777); e == nil {
						e = os.WriteFile(path, []byte(text), 0666)
					}
				}
				if e != nil {
					return e
				}
			case "list":
				var entry, label string
				_ = json.Unmarshal(operation.Get("entry"), &entry)
				_ = json.Unmarshal(operation.Get("label"), &label)
				reason, err := gate.DisabledReason(scope)
				if err != nil {
					return err
				}
				snapshot := map[string]any{"label": label, "entry": entry, "reason": nil, "enabled": reason == ""}
				if reason != "" {
					snapshot["reason"] = reason
				}
				if entry == "discovery" {
					f, err := service.BeginDefinitions(scope, nil)
					if err != nil {
						return err
					}
					values, err := f.Await(scope)
					if err != nil {
						return err
					}
					defs := []any{}
					for _, value := range values {
						defs = append(defs, value.Object)
					}
					snapshot["workflows"] = defs
				} else {
					var commands []*workflowcommands.Command
					if entry == "commands" {
						f, err := service.BeginCommands(scope, state.Snapshot().OriginalCWD)
						if err != nil {
							return err
						}
						commands, err = f.Await(scope)
						if err != nil {
							return err
						}
					} else {
						f, err := service.BeginFreshCommands(scope, nil)
						if err != nil {
							return err
						}
						commands, err = f.Await(scope)
						if err != nil {
							return err
						}
					}
					observed := []any{}
					var args []json.RawMessage
					_ = json.Unmarshal(operation.Get("args"), &args)
					for _, command := range commands {
						enabled, err := command.IsEnabled(scope)
						if err != nil {
							return err
						}
						if !enabled {
							continue
						}
						descriptor, err := command.Descriptor(scope)
						if err != nil {
							return err
						}
						keys, _ := json.Marshal(command.Keys())
						value := protocol.NewJSONObject(protocol.JSONField{Name: "keys", Value: keys})
						for _, property := range descriptor.OwnProperties() {
							var key string
							_ = json.Unmarshal(property.NameJSON, &key)
							value.Set(key, property.Value)
						}
						name, err := command.UserFacingName(scope)
						if err != nil {
							return err
						}
						value.Set("enabled", []byte("true"))
						value.Set("userFacingName", quote(name))
						prompts := []any{}
						for _, arg := range args {
							f, err := command.BeginPrompt(scope, arg)
							if err != nil {
								return err
							}
							output, err := f.Await(scope)
							if err != nil {
								return err
							}
							prompts = append(prompts, protocol.NewJSONObject(protocol.JSONField{Name: "args", Value: arg}, protocol.JSONField{Name: "output", Value: output}))
						}
						promptsRaw, _ := json.Marshal(prompts)
						value.Set("prompts", promptsRaw)
						observed = append(observed, value)
					}
					snapshot["commands"] = observed
				}
				snapshots = append(snapshots, snapshot)
			default:
				return errors.New("unknown original operation")
			}
			trace = append(trace, map[string]any{"event": "operation:return", "original": operation})
		}
		return nil
	})
	return finish(e)
}
