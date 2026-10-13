package installationdiagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Warning struct {
	Issue string `json:"issue"`
	Fix   string `json:"fix"`
}
type Installation struct {
	Type string `json:"type"`
	Path string `json:"path"`
}
type RipgrepStatus struct {
	Working    bool    `json:"working"`
	Mode       string  `json:"mode"`
	SystemPath *string `json:"systemPath"`
}
type Diagnostic struct {
	InstallationType      string         `json:"installationType"`
	Version               string         `json:"version"`
	InstallationPath      string         `json:"installationPath"`
	InvokedBinary         string         `json:"invokedBinary"`
	ConfigInstallMethod   string         `json:"configInstallMethod"`
	AutoUpdates           string         `json:"autoUpdates"`
	HasUpdatePermissions  *bool          `json:"hasUpdatePermissions"`
	MultipleInstallations []Installation `json:"multipleInstallations"`
	Warnings              []Warning      `json:"warnings"`
	PackageManager        string         `json:"packageManager,omitempty"`
	Ripgrep               RipgrepStatus  `json:"ripgrepStatus"`
}
type Options struct {
	PackageOptions
	Packages                                                         *Packages
	Home, CWD, ConfigDir, ManagedRoot, Version, PackageName, Invoked string
	Bundled                                                          bool
	Environment                                                      map[string]string
	Global                                                           protocol.JSONObject
	Stat                                                             func(string) (fs.FileInfo, error)
	Realpath                                                         func(string) (string, error)
	Which                                                            func(context.Context, string) (string, error)
	Alias                                                            func() (string, bool)
	ValidAlias                                                       func() bool
	UpdatePermissions                                                func(context.Context) bool
	Ripgrep                                                          RipgrepStatus
	LinuxGlobPatterns                                                []string
}
type Service struct {
	options  Options
	packages *Packages
}

func New(options Options) *Service {
	packages := options.Packages
	if packages == nil {
		packages = NewPackages(options.PackageOptions)
	}
	return &Service{options: options, packages: packages}
}
func (s *Service) join(paths ...string) string {
	if s.options.Platform == "windows" {
		return nativepaths.JoinWindows(paths...)
	}
	return filepath.Join(paths...)
}
func (s *Service) exists(path string) bool {
	if s.options.Stat == nil {
		return false
	}
	_, e := s.options.Stat(path)
	return e == nil
}
func (s *Service) localExists() bool {
	directory := s.options.ConfigDir
	if directory == "" {
		directory = s.join(s.options.Home, ".claude")
	}
	return s.exists(s.join(directory, "local", "node_modules", ".bin", "claude"))
}
func (s *Service) run(ctx context.Context, name string, args []string, opts RunOptions) ProcessResult {
	if s.options.Run == nil {
		return ProcessResult{Code: 1}
	}
	r, e := s.options.Run(ctx, name, args, opts)
	if e != nil {
		return ProcessResult{Code: 1}
	}
	return r
}
func (s *Service) installationType(ctx context.Context) string {
	o := s.options
	if o.Environment["NODE_ENV"] == "development" {
		return "development"
	}
	if o.Bundled {
		if s.packages.Homebrew() || s.packages.Winget() || s.packages.Mise() || s.packages.Asdf() || s.packages.Pacman(ctx) || s.packages.Deb(ctx) || s.packages.Rpm(ctx) || s.packages.Apk(ctx) {
			return "package-manager"
		}
		return "native"
	}
	if strings.Contains(o.Invoked, "/.claude/local/node_modules/") {
		return "npm-local"
	}
	invoked := o.Invoked
	if o.Platform == "windows" {
		invoked = strings.ReplaceAll(invoked, `\`, "/")
	}
	for _, path := range []string{"/usr/local/lib/node_modules", "/usr/lib/node_modules", "/opt/homebrew/lib/node_modules", "/opt/homebrew/bin", "/usr/local/bin", "/.nvm/versions/node/", "/npm/", "/nvm/"} {
		if strings.Contains(invoked, path) {
			return "npm-global"
		}
	}
	r := s.run(ctx, "npm", []string{"config", "get", "prefix"}, RunOptions{UseCWD: true, Shell: true})
	prefix := protocol.TrimJavaScriptString(r.Stdout)
	if r.Code == 0 && prefix != "" && strings.HasPrefix(invoked, prefix) {
		return "npm-global"
	}
	return "unknown"
}
func (s *Service) installationPath(ctx context.Context) string {
	if s.options.Environment["NODE_ENV"] == "development" {
		return s.options.CWD
	}
	if !s.options.Bundled {
		if s.options.Argv0 != "" {
			return s.options.Argv0
		}
		return "unknown"
	}
	if s.options.Realpath != nil {
		if path, e := s.options.Realpath(s.options.Executable); e == nil {
			return path
		}
	}
	if s.options.Which != nil {
		if path, e := s.options.Which(ctx, "claude"); e == nil && path != "" {
			return path
		}
	}
	path := s.join(s.options.Home, ".local", "bin", "claude")
	if s.exists(path) {
		return path
	}
	return "native"
}
func (s *Service) multiple(ctx context.Context) []Installation {
	all := []Installation{}
	if s.localExists() {
		all = append(all, Installation{"npm-local", s.join(s.options.Home, ".claude", "local")})
	}
	r := s.run(ctx, "npm", []string{"-g", "config", "get", "prefix"}, RunOptions{UseCWD: true})
	if r.Code == 0 && r.Stdout != "" {
		prefix := protocol.TrimJavaScriptString(r.Stdout)
		bin := s.join(prefix, "bin", "claude")
		if s.options.Platform == "windows" {
			bin = s.join(prefix, "claude")
		}
		if s.exists(bin) {
			sameBrew := false
			if s.options.Realpath != nil {
				if path, e := s.options.Realpath(bin); e == nil && strings.Contains(path, "/Caskroom/") {
					sameBrew = s.packages.Homebrew()
				}
			}
			if !sameBrew {
				all = append(all, Installation{"npm-global", bin})
			}
		} else {
			names := []string{"@anthropic-ai/claude-code"}
			if s.options.PackageName != "" && s.options.PackageName != names[0] {
				names = append(names, s.options.PackageName)
			}
			for _, name := range names {
				path := s.join(prefix, "lib", "node_modules", name)
				if s.options.Platform == "windows" {
					path = s.join(prefix, "node_modules", name)
				}
				if s.exists(path) {
					all = append(all, Installation{"npm-global-orphan", path})
				}
			}
		}
	}
	native := s.join(s.options.Home, ".local", "bin", "claude")
	if s.exists(native) {
		all = append(all, Installation{"native", native})
	}
	if s.options.Global.String("installMethod") == "native" {
		data := s.join(s.options.Home, ".local", "share", "claude")
		if s.exists(data) {
			have := false
			for _, i := range all {
				if i.Type == "native" {
					have = true
				}
			}
			if !have {
				all = append(all, Installation{"native", data})
			}
		}
	}
	return all
}
func truthy(value string) bool {
	switch strings.ToLower(protocol.TrimJavaScriptString(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func (s *Service) disabledReason() string {
	env := s.options.Environment
	if env["NODE_ENV"] == "development" {
		return "development build"
	}
	if truthy(env["DISABLE_AUTOUPDATER"]) {
		return "DISABLE_AUTOUPDATER set"
	}
	if env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "" {
		return "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC set"
	}
	global := s.options.Global
	if string(global.Get("autoUpdates")) == "false" && (global.String("installMethod") != "native" || string(global.Get("autoUpdatesProtectedForNative")) != "true") {
		return "config"
	}
	return ""
}
func (s *Service) managedWarnings() []Warning {
	warnings := []Warning{}
	if s.options.ReadFile == nil {
		return warnings
	}
	data, e := s.options.ReadFile(s.join(s.options.ManagedRoot, "managed-settings.json"))
	if e != nil {
		return warnings
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		return warnings
	}
	raw, ok := object["strictPluginOnlyCustomization"]
	if !ok {
		return warnings
	}
	if string(raw) == "true" || string(raw) == "false" {
		return warnings
	}
	var array []json.RawMessage
	if string(raw) != "null" && json.Unmarshal(raw, &array) == nil {
		unknown := []string{}
		for _, v := range array {
			var value string
			if json.Unmarshal(v, &value) == nil && value != "skills" && value != "agents" && value != "hooks" && value != "mcp" {
				unknown = append(unknown, value)
			}
		}
		if len(unknown) > 0 {
			warnings = append(warnings, Warning{fmt.Sprintf("managed-settings.json: strictPluginOnlyCustomization has %d value(s) this client doesn't recognize: %s", len(unknown), strings.Join(unknown, ", ")), "These are silently ignored (forwards-compat). Known surfaces for this version: skills, agents, hooks, mcp. Either remove them, or this client is older than the managed-settings intended."})
		}
		return warnings
	}
	typeName := "object"
	var str string
	var number float64
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &str) == nil {
		typeName = "string"
	} else if json.Unmarshal(raw, &number) == nil && string(raw) != "null" {
		typeName = "number"
	}
	return append(warnings, Warning{"managed-settings.json: strictPluginOnlyCustomization has an invalid value (expected true or an array, got " + typeName + ")", "The field is silently ignored (schema .catch rescues it). Set it to true, or an array of: skills, agents, hooks, mcp."})
}

var trailingSlashes = regexp.MustCompile(`[/\\]+$`)

func (s *Service) configurationWarnings(ctx context.Context, kind string) []Warning {
	warnings := s.managedWarnings()
	if kind == "development" {
		return warnings
	}
	o := s.options
	if kind == "native" {
		bin := s.join(o.Home, ".local", "bin")
		delimiter := ":"
		normalized := bin
		if o.Platform == "windows" {
			delimiter = ";"
			normalized = strings.ReplaceAll(bin, `\`, "/")
		}
		present := false
		for _, p := range strings.Split(o.Environment["PATH"], delimiter) {
			raw := trailingSlashes.ReplaceAllString(p, "")
			if o.Platform == "windows" {
				p = strings.ReplaceAll(p, `\`, "/")
			}
			p = strings.TrimRight(p, "/")
			if p == normalized || raw == "~/.local/bin" || raw == "$HOME/.local/bin" {
				present = true
			}
		}
		if !present {
			if o.Platform == "windows" {
				warnings = append(warnings, Warning{"Native installation exists but " + strings.ReplaceAll(bin, "/", `\`) + " is not in your PATH", "Add it by opening: System Properties → Environment Variables → Edit User PATH → New → Add the path above. Then restart your terminal."})
			} else {
				display := "your shell config file"
				shell := o.Environment["SHELL"]
				if strings.Contains(shell, "zsh") {
					dir := o.Environment["ZDOTDIR"]
					if dir == "" {
						dir = o.Home
					}
					display = s.join(dir, ".zshrc")
				} else if strings.Contains(shell, "bash") {
					display = s.join(o.Home, ".bashrc")
				} else if strings.Contains(shell, "fish") {
					display = s.join(o.Home, ".config/fish/config.fish")
				}
				display = strings.Replace(display, o.Home, "~", 1)
				warnings = append(warnings, Warning{"Native installation exists but ~/.local/bin is not in your PATH", `Run: echo 'export PATH="$HOME/.local/bin:$PATH"' >> ` + display + " then open a new terminal or run: source " + display})
			}
		}
	}
	method := "undefined"
	if raw := o.Global.Get("installMethod"); raw != nil {
		method = o.Global.String("installMethod")
		if string(raw) == "null" {
			method = "null"
		}
	}
	if !truthy(o.Environment["DISABLE_INSTALLATION_CHECKS"]) {
		if kind == "npm-local" && method != "local" {
			warnings = append(warnings, Warning{"Running from local installation but config install method is '" + method + "'", "Consider using native installation: claude install"})
		}
		if kind == "native" && method != "native" {
			warnings = append(warnings, Warning{"Running native installation but config install method is '" + method + "'", "Run claude install to update configuration"})
		}
	}
	if kind == "npm-global" && s.localExists() {
		warnings = append(warnings, Warning{"Local installation exists but not being used", "Consider using native installation: claude install"})
	}
	if kind == "npm-local" {
		which := ""
		if o.Which != nil {
			which, _ = o.Which(ctx, "claude")
		}
		valid := o.ValidAlias != nil && o.ValidAlias()
		if which == "" && !valid {
			alias := ""
			if o.Alias != nil {
				alias, _ = o.Alias()
			}
			fix := `Create alias: alias claude="~/.claude/local/claude"`
			if alias != "" {
				fix = `Alias exists but points to invalid target: ` + alias + `. Update alias: alias claude="~/.claude/local/claude"`
			}
			warnings = append(warnings, Warning{"Local installation not accessible", fix})
		}
	}
	return warnings
}
func (s *Service) Diagnose(ctx context.Context) Diagnostic {
	kind := s.installationType(ctx)
	version := s.options.Version
	if version == "" {
		version = "unknown"
	}
	invoked := s.options.Invoked
	if s.options.Bundled {
		invoked = s.options.Executable
	}
	if invoked == "" {
		invoked = "unknown"
	}
	method := s.options.Global.String("installMethod")
	if method == "" {
		method = "not set"
	}
	updates := "enabled"
	reason := s.disabledReason()
	if reason != "" {
		updates = "disabled (" + reason + ")"
	}
	d := Diagnostic{InstallationType: kind, Version: version, InstallationPath: s.installationPath(ctx), InvokedBinary: invoked, ConfigInstallMethod: method, AutoUpdates: updates, MultipleInstallations: s.multiple(ctx), Warnings: s.configurationWarnings(ctx, kind), Ripgrep: s.options.Ripgrep}
	if s.options.Platform == "linux" && len(s.options.LinuxGlobPatterns) > 0 {
		patterns := s.options.LinuxGlobPatterns
		n := len(patterns)
		display := strings.Join(patterns[:min(n, 3)], ", ")
		if n > 3 {
			display += fmt.Sprintf(" (%d more)", n-3)
		}
		d.Warnings = append(d.Warnings, Warning{"Glob patterns in sandbox permission rules are not fully supported on Linux", fmt.Sprintf("Found %d pattern(s): %s. On Linux, glob patterns in Edit/Read rules will be ignored.", n, display)})
	}
	if kind == "native" {
		for _, i := range d.MultipleInstallations {
			switch i.Type {
			case "npm-global":
				command := "npm -g uninstall @anthropic-ai/claude-code"
				if s.options.PackageName != "" && s.options.PackageName != "@anthropic-ai/claude-code" {
					command += " && npm -g uninstall " + s.options.PackageName
				}
				d.Warnings = append(d.Warnings, Warning{"Leftover npm global installation at " + i.Path, "Run: " + command})
			case "npm-global-orphan", "npm-local":
				label := "Orphaned npm global package at "
				if i.Type == "npm-local" {
					label = "Leftover npm local installation at "
				}
				fix := "Run: rm -rf " + i.Path
				if s.options.Platform == "windows" {
					fix = `Run: rmdir /s /q "` + i.Path + `"`
				}
				d.Warnings = append(d.Warnings, Warning{label + i.Path, fix})
			}
		}
	}
	if kind == "npm-global" && s.options.UpdatePermissions != nil {
		allowed := s.options.UpdatePermissions(ctx)
		d.HasUpdatePermissions = &allowed
		if !allowed && reason == "" {
			d.Warnings = append(d.Warnings, Warning{"Insufficient permissions for auto-updates", "Do one of: (1) Re-install node without sudo, or (2) Use `claude install` for native installation"})
		}
	}
	if kind == "package-manager" {
		d.PackageManager = s.packages.Detect(ctx)
	}
	return d
}
