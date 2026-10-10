package clibootstrap

import (
	"context"
	"encoding/json"
	"os/exec"
	"slices"

	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
)

type NativeSkillOptions struct {
	AdditionalDirectories []string
	Bare                  bool
	InlinePluginCount     int
}

// SkillCommands binds discovery to the same settings, policy and installed
// plugin lifetime as the process. Invocation remains owned by its Harness.
func (p *NativeProcessSettings) SkillCommands(o NativeSkillOptions) (*skillcommands.Service, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return nil, err
	}
	n := p.n
	sources := append([]string{}, enabledSources(p.sources.AllowedSettingSources)...)
	settings := func(ctx context.Context) (protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		value, err := p.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}
	roots, err := skillregistry.New(skillregistry.Options{
		Home: n.Home, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], CWD: n.CWD, ProjectRoot: n.CWD,
		FS: skillcommands.NativeFiles{}, Environment: func(key string) string { return n.Environment[key] }, Settings: settings, Plugins: p,
	})
	if err != nil {
		return nil, err
	}
	directories := append([]string{}, o.AdditionalDirectories...)
	service, err := skillcommands.New(skillcommands.Options{
		Home: n.Home, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], CWD: n.CWD, ManagedDir: n.ManagedRoot,
		Roots: roots, Files: skillcommands.NativeFiles{}, Environment: func(key string) string { return n.Environment[key] },
		EnabledSource: func(ctx context.Context, source string) (bool, error) {
			return slices.Contains(sources, source), ctx.Err()
		},
		SkillsLocked: func(ctx context.Context) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			policy, err := n.ResolvePolicy()
			if err != nil || policy == nil {
				return false, err
			}
			value := policy.Get("strictPluginOnlyCustomization")
			if string(value) == "true" {
				return true, nil
			}
			var restricted []string
			_ = json.Unmarshal(value, &restricted)
			return slices.Contains(restricted, "skills"), nil
		},
		AdditionalDirectories: func(context.Context) []string { return append([]string{}, directories...) },
		Bare:                  func() bool { return o.Bare }, InlinePluginCount: func() int { return o.InlinePluginCount },
		Plugins: func(ctx context.Context) ([]pluginregistry.Loaded, error) {
			result, err := p.LoadPlugins(ctx, "", false)
			return result.Enabled, err
		},
		ResolveModel: func(ctx context.Context, name string) (string, error) {
			merged, err := settings(ctx)
			if err != nil {
				return "", err
			}
			selector, err := modelselection.NewParsing(modelselection.Options{Environment: n.Environment, Settings: merged})
			if err != nil {
				return "", err
			}
			value, err := selector.Parse(quoteMain(name))
			if err != nil {
				return "", err
			}
			return decodeNativeModel(value)
		},
		PluginDataDirectory: func(ctx context.Context, id string) (string, error) {
			return nativePluginDataDirectory(n, ctx, id)
		},
		Gitignored: func(ctx context.Context, path, cwd string) (bool, error) {
			command := exec.CommandContext(ctx, "git", "check-ignore", path)
			command.Dir, command.Env = cwd, nativeSkillEnvironment(n.Environment)
			err := command.Run()
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			// Source execFileNoThrow treats unavailable/non-repository git as
			// not ignored; only exit code zero means ignored.
			return err == nil, nil
		},
	})
	if err == nil {
		p.skills = append(p.skills, service)
	}
	return service, err
}

func nativeSkillEnvironment(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+environment[key])
	}
	return values
}
