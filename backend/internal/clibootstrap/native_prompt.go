package clibootstrap

import (
	"context"
	"encoding/json"
	weburl "github.com/nlnwa/whatwg-url/url"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func (m *MainPrint) nativeMemory() (*usercontext.ExplicitUserMemory, *usercontext.MemoryPaths, error) {
	n := m.options.Native
	files := nativeMemoryFiles{n: n}
	paths := &usercontext.MemoryPaths{Environment: m.environment, Home: n.Home, ConfigHome: n.ConfigHomeDirectory(), Files: files, Realpath: files.Realpath, SettingsForSource: m.nativeSDKSourceSettings, ProjectRoot: func() string {
		root := usercontext.GitRoot(files, n.CWD)
		if root == "" {
			return n.CWD
		}
		return root
	}}
	if n.NativeProcess {
		paths.NativeConfigDirectory = n.nativeConfigHomePath
	}
	settings, err := m.nativeSDKInitialSettings()
	if err != nil {
		return nil, nil, err
	}
	var excludes []string
	if settings != nil && settings.Get("claudeMdExcludes") != nil {
		if err = json.Unmarshal(settings.Get("claudeMdExcludes"), &excludes); err != nil {
			return nil, nil, err
		}
	}
	var matcher *usercontext.ExclusionMatcher
	if len(excludes) > 0 {
		matcher, err = usercontext.NewExclusionMatcher(filepath.EvalSymlinks, m.options.Platform)
		if err != nil {
			return nil, nil, err
		}
	}
	key := usercontext.CanonicalGitRoot(files, filepath.EvalSymlinks, n.CWD)
	if key == "" {
		key = n.CWD
	}
	if m.options.Platform == "windows" {
		key = strings.ReplaceAll(key, "\\", "/")
	}
	project, err := m.Global.GetProject(key)
	if err != nil {
		return nil, nil, err
	}
	normal := &usercontext.NormalMemoryDiscovery{CWD: n.CWD, Home: n.Home, ManagedRoot: n.ManagedRoot, EnabledSources: m.options.Arguments.Sources.EnabledSettingSources, NativeProcess: true, AutoMemoryEnabled: settings == nil || systemprompt.AutoMemoryEnabled(m.environment, *settings), AutoMemoryDirectory: paths.Directory, Excludes: excludes, Matcher: matcher, Realpath: files.Realpath, ReadDir: files.ReadDir, IncludeExternal: string(project.Get("hasClaudeMdExternalIncludesApproved")) == "true", AdditionalDirectoriesEnabled: envTruthyNative(n.Environment["CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"])}
	if n.NativeProcess {
		normal.NativeConfigDirectory = n.nativeConfigHomePath
	}
	return &usercontext.ExplicitUserMemory{ConfigHome: n.ConfigHomeDirectory(), Profile: usercontext.NormalEmptyMemoryProfile, Files: files, Normal: normal}, paths, nil
}

func (m *MainPrint) nativeSystemPrompt(uc *usercontext.Service, paths *usercontext.MemoryPaths) (*systemprompt.Service, error) {
	n := m.options.Native
	var style *systemprompt.StyleLoader
	style, err := nativeStyleLoader(n, m.environment, m.options.Platform, m.options.Arguments.Sources, m.nativeSDKInitialSettings, func(ctx context.Context) ([]systemprompt.StyleConfig, error) {
		plugins, err := m.nativeSettings.LoadPlugins(ctx, "", false)
		if err != nil {
			return nil, err
		}
		return style.PluginStyles(ctx, plugins.Enabled)
	})
	if err != nil {
		return nil, err
	}
	m.styles = style
	return systemprompt.New(systemprompt.Options{AdditionalDirectories: append([]string{}, m.nativePermissionInitial.AdditionalDirectories...), Environment: m.environment, CWD: m.workingDirectory, Model: m.options.Arguments.Model, Platform: m.options.Platform, OSInfo: nativeOSInfo, IsGit: func(context.Context) (bool, error) {
		return usercontext.GitRoot(mainSettingsFiles{n: n}, m.workingDirectory()) != "", nil
	}, InitialSettings: func() (protocol.JSONObject, error) {
		value, err := m.nativeSDKInitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}, SessionStartDate: uc.GetSessionStartDate, NonInteractive: func() bool { return true }, SkillCommands: func(context.Context, string) ([]systemprompt.Command, error) {
		catalog := m.Registry.Catalog()
		commands := make([]systemprompt.Command, 0, len(catalog.Skills))
		for _, name := range catalog.Skills {
			commands = append(commands, systemprompt.Command{Name: name})
		}
		return commands, nil
	}, OutputStyle: style.Resolve, GlobalCacheScope: func() bool { return nativeGlobalCacheScope(n.Environment) }, AutoMemory: &systemprompt.AutoMemory{Directory: paths.Directory, Mkdir: nativeMemoryFiles{n: n}.MkdirAll}})
}

func nativeGlobalCacheScope(env map[string]string) bool {
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"} {
		if envTruthyNative(env[key]) {
			return false
		}
	}
	base := env["ANTHROPIC_BASE_URL"]
	if base == "" {
		return true
	}
	parsed, err := weburl.Parse(base)
	return err == nil && (parsed.Host() == "api.anthropic.com" || env["USER_TYPE"] == "ant" && parsed.Host() == "api-staging.anthropic.com")
}

func nativeStyleLoader(n *NativeBoundary, environment *clistartupenv.Environment, platform string, sources State, initialSettings func() (*protocol.JSONObject, error), plugins ...func(context.Context) ([]systemprompt.StyleConfig, error)) (*systemprompt.StyleLoader, error) {
	if len(plugins) > 1 || len(plugins) == 1 && plugins[0] == nil {
		return nil, mainCap("one native plugin output style producer")
	}
	pluginStyles := func(context.Context) ([]systemprompt.StyleConfig, error) {
		absent, err := n.nativePluginsAbsent()
		if err != nil {
			return nil, err
		}
		if !absent {
			return nil, mainCap("active output style plugin loader")
		}
		settings, err := initialSettings()
		if err != nil {
			return nil, err
		}
		if settings != nil && (settings.Get("enabledPlugins") != nil || settings.Get("extraKnownMarketplaces") != nil) {
			return nil, mainCap("configured output style plugins")
		}
		return []systemprompt.StyleConfig{}, nil
	}
	if len(plugins) == 1 {
		pluginStyles = plugins[0]
	}
	style, err := systemprompt.NewStyleLoader(systemprompt.StyleLoaderOptions{
		Environment: environment, Platform: platform, Home: n.Home,
		ConfigHome: n.ConfigHomeDirectory(), ManagedRoot: n.ManagedRoot,
		Files: mainSettingsFiles{n: n}, ReadDir: func(path string) ([]fs.DirEntry, error) {
			if err := n.check("readdir", path); err != nil {
				return nil, err
			}
			return usercontext.NativeReadDir(path)
		}, Realpath: filepath.EvalSymlinks, Lstat: func(path string) (fs.FileInfo, error) {
			if err := n.check("lstat", path); err != nil {
				return nil, err
			}
			return os.Lstat(path)
		}, ProjectRoot: func() string { return n.CWD }, SourceEnabled: func(source string) bool {
			for _, enabled := range sources.EnabledSettingSources {
				if source == enabled {
					return true
				}
			}
			return false
		}, Settings: func() (protocol.JSONObject, error) {
			value, err := initialSettings()
			if value == nil {
				return protocol.NewJSONObject(), err
			}
			return value.Clone(), err
		},
		PluginStyles: pluginStyles,
	})
	return style, err
}
