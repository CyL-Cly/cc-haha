package ideintegration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/releasenotes"
)

type ExtensionProcessResult struct {
	Stdout, Stderr, Error string
	Code                  int
}
type ExtensionOptions struct {
	Home, Platform, Version string
	Environment             map[string]string
	Run                     func(context.Context, string, []string, map[string]string, time.Duration) (ExtensionProcessResult, error)
	RunShell                func(context.Context, string) (ExtensionProcessResult, error)
	Stat                    func(string) (fs.FileInfo, error)
	ReadDir                 func(string) ([]fs.DirEntry, error)
	ParentCommand           func(context.Context) string
	Sleep                   func(context.Context, time.Duration) error
}
type ExtensionStatus struct {
	Installed bool    `json:"installed"`
	Error     *string `json:"error"`
	Version   *string `json:"installedVersion"`
	IDEType   string  `json:"ideType"`
}
type extensionProbe struct {
	done      chan struct{}
	installed bool
}
type Extensions struct {
	options ExtensionOptions
	mu      sync.Mutex
	plugins map[string]*extensionProbe
}

func NewExtensions(options ExtensionOptions) *Extensions {
	env := map[string]string{}
	for k, v := range options.Environment {
		env[k] = v
	}
	options.Environment = env
	if options.Sleep == nil {
		options.Sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
	}
	return &Extensions{options: options, plugins: map[string]*extensionProbe{}}
}
func VSCodeIDE(kind string) bool    { return kind == "vscode" || kind == "cursor" || kind == "windsurf" }
func JetBrainsIDE(kind string) bool { _, ok := displayNames[kind]; return ok && !VSCodeIDE(kind) }
func IDEName(kind string) string {
	if name := displayNames[kind]; name != "" {
		return name
	}
	return "IDE"
}
func AutoInstall(global *bool, env map[string]string) bool {
	return !TruthyEnv(env["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"]) && (global == nil || *global)
}

func (s *Extensions) command(ctx context.Context, kind string) string {
	if s.options.ParentCommand != nil {
		if path := s.options.ParentCommand(ctx); path != "" {
			if _, e := s.options.Stat(path); e == nil {
				return path
			}
		}
	}
	if !VSCodeIDE(kind) {
		return ""
	}
	command := map[string]string{"vscode": "code", "cursor": "cursor", "windsurf": "windsurf"}[kind]
	if s.options.Platform == "windows" {
		command += ".cmd"
	}
	return command
}
func (s *Extensions) installationEnvironment() map[string]string {
	env := map[string]string{}
	for k, v := range s.options.Environment {
		env[k] = v
	}
	if s.options.Platform == "linux" {
		env["DISPLAY"] = ""
	}
	return env
}
func (s *Extensions) Installed(ctx context.Context, kind string) bool {
	if VSCodeIDE(kind) {
		command := s.command(ctx, kind)
		if command == "" {
			return false
		}
		out, e := s.options.Run(ctx, command, []string{"--list-extensions"}, s.installationEnvironment(), 10*time.Minute)
		id := "anthropic.claude-code"
		if s.options.Environment["USER_TYPE"] == "ant" {
			id = "anthropic.claude-code-internal"
		}
		return e == nil && strings.Contains(out.Stdout, id)
	}
	if !JetBrainsIDE(kind) {
		return false
	}
	s.mu.Lock()
	entry := s.plugins[kind]
	if entry == nil {
		entry = &extensionProbe{done: make(chan struct{})}
		s.plugins[kind] = entry
		s.mu.Unlock()
		entry.installed = s.pluginInstalled(kind)
		close(entry.done)
	} else {
		s.mu.Unlock()
	}
	select {
	case <-entry.done:
		return entry.installed
	case <-ctx.Done():
		return false
	}
}
func (s *Extensions) Install(ctx context.Context, kind string) ExtensionStatus {
	status := ExtensionStatus{IDEType: kind}
	version, e := s.install(ctx, kind)
	if e != nil {
		value := e.Error()
		status.Error = &value
		return status
	}
	status.Installed = true
	status.Version = version
	return status
}
func (s *Extensions) install(ctx context.Context, kind string) (*string, error) {
	if !VSCodeIDE(kind) {
		return nil, nil
	} // Source native builds show the JetBrains marketplace notice.
	command := s.command(ctx, kind)
	if command == "" {
		return nil, nil
	}
	if s.options.Environment["USER_TYPE"] == "ant" {
		return nil, errors.New("IDE internal Artifactory installation requires the internal distribution")
	}
	out, e := s.options.Run(ctx, command, []string{"--list-extensions", "--show-versions"}, s.installationEnvironment(), 10*time.Minute)
	if e != nil {
		return nil, e
	}
	var version *string
	for _, line := range strings.Split(out.Stdout, "\n") {
		parts := strings.Split(line, "@")
		if len(parts) > 1 && parts[0] == "anthropic.claude-code" && parts[1] != "" {
			value := parts[1]
			version = &value
			break
		}
	}
	install := version == nil
	if version != nil {
		order, e := releasenotes.Compare(*version, s.options.Version)
		if e != nil {
			return nil, e
		}
		install = order < 0
	}
	if install {
		if e := s.options.Sleep(ctx, 500*time.Millisecond); e != nil {
			return nil, e
		}
		out, e = s.options.Run(ctx, command, []string{"--force", "--install-extension", "anthropic.claude-code"}, s.installationEnvironment(), 10*time.Minute)
		if e != nil {
			return nil, e
		}
		if out.Code != 0 {
			return nil, fmt.Errorf("%d: %s %s", out.Code, out.Error, out.Stderr)
		}
		value := s.options.Version
		version = &value
	}
	return version, nil
}

var pluginPatterns = map[string][]string{"pycharm": {"PyCharm"}, "intellij": {"IntelliJIdea", "IdeaIC"}, "webstorm": {"WebStorm"}, "phpstorm": {"PhpStorm"}, "rubymine": {"RubyMine"}, "clion": {"CLion"}, "goland": {"GoLand"}, "rider": {"Rider"}, "datagrip": {"DataGrip"}, "appcode": {"AppCode"}, "dataspell": {"DataSpell"}, "aqua": {"Aqua"}, "gateway": {"Gateway"}, "fleet": {"Fleet"}, "androidstudio": {"AndroidStudio"}}

func (s *Extensions) join(parts ...string) string {
	if s.options.Platform == "windows" {
		return nativepaths.JoinWindows(parts...)
	}
	return filepath.Join(parts...)
}
func (s *Extensions) PluginDirectories(kind string) []string {
	home := s.options.Home
	dirs := []string{}
	patterns := pluginPatterns[strings.ToLower(kind)]
	if len(patterns) == 0 {
		return dirs
	}
	switch s.options.Platform {
	case "macos", "darwin":
		dirs = append(dirs, s.join(home, "Library", "Application Support", "JetBrains"), s.join(home, "Library", "Application Support"))
		if kind == "androidstudio" {
			dirs = append(dirs, s.join(home, "Library", "Application Support", "Google"))
		}
	case "windows":
		app, local := s.options.Environment["APPDATA"], s.options.Environment["LOCALAPPDATA"]
		if app == "" {
			app = s.join(home, "AppData", "Roaming")
		}
		if local == "" {
			local = s.join(home, "AppData", "Local")
		}
		dirs = append(dirs, s.join(app, "JetBrains"), s.join(local, "JetBrains"), app)
		if kind == "androidstudio" {
			dirs = append(dirs, s.join(local, "Google"))
		}
	case "linux":
		dirs = append(dirs, s.join(home, ".config", "JetBrains"), s.join(home, ".local", "share", "JetBrains"))
		for _, pattern := range patterns {
			dirs = append(dirs, s.join(home, "."+pattern))
		}
		if kind == "androidstudio" {
			dirs = append(dirs, s.join(home, ".config", "Google"))
		}
	}
	return dirs
}
func (s *Extensions) pluginInstalled(kind string) bool {
	seen := map[string]bool{}
	for _, base := range s.PluginDirectories(kind) {
		entries, e := s.options.ReadDir(base)
		if e != nil {
			continue
		}
		for _, pattern := range pluginPatterns[strings.ToLower(kind)] {
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), pattern) || !entry.IsDir() && entry.Type()&fs.ModeSymlink == 0 {
					continue
				}
				path := s.join(base, entry.Name())
				if s.options.Platform != "linux" {
					path = s.join(path, "plugins")
					if _, e := s.options.Stat(path); e != nil {
						continue
					}
				}
				if seen[path] {
					continue
				}
				seen[path] = true
				if _, e := s.options.Stat(s.join(path, "claude-code-jetbrains-plugin")); e == nil {
					return true
				}
			}
		}
	}
	return false
}
