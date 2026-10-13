// Package windowshell binds the Source shell discovery and invocation policy
// to explicit native process observations. It never reads the caller's HOME or
// process environment implicitly.
package windowshell

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Dependencies struct {
	PathExists          func(context.Context, string) bool
	FileExists          func(context.Context, string) bool
	Where               func(context.Context, string) (string, error)
	Which               func(context.Context, string) string
	ReadWindowsPwshHome func(context.Context) string
}

type Availability struct {
	Bash, PowerShell bool
}

type Resolver struct {
	platform, cwd string
	environment   map[string]string
	dependencies  Dependencies
	mu            sync.Mutex
	bashResolved  bool
	bash          string
	psResolved    bool
	ps            string
}

func New(platform, cwd string, environment map[string]string, dependencies Dependencies) *Resolver {
	env := make(map[string]string, len(environment))
	for name, value := range environment {
		env[name] = value
	}
	return &Resolver{platform: platform, cwd: cwd, environment: env, dependencies: dependencies}
}

func IsWindows(platform string) bool { return platform == "windows" || platform == "win32" }

func Truthy(value string) bool {
	switch strings.ToLower(protocol.TrimJavaScriptString(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func DefinedFalsy(value string, defined bool) bool {
	if !defined {
		return false
	}
	switch strings.ToLower(protocol.TrimJavaScriptString(value)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// ResolveAvailability is Source shellToolUtils.resolveShellToolAvailability:
// PowerShell is advertised as a graceful fallback even when no binary exists.
func ResolveAvailability(platform, gitBashPath, userType string, setting string, defined bool) Availability {
	if !IsWindows(platform) {
		return Availability{Bash: true}
	}
	bash := gitBashPath != ""
	return Availability{Bash: bash, PowerShell: !DefinedFalsy(setting, defined) && (userType == "ant" || !bash || Truthy(setting))}
}

func DefaultShell(configured string, availability Availability) string {
	if configured == "" {
		configured = "bash"
	}
	if configured == "powershell" {
		if availability.PowerShell {
			return configured
		}
		if availability.Bash {
			return "bash"
		}
	} else {
		if availability.Bash {
			return "bash"
		}
		if availability.PowerShell {
			return "powershell"
		}
	}
	return ""
}

func (r *Resolver) Availability(ctx context.Context) Availability {
	if !IsWindows(r.platform) {
		return Availability{Bash: true}
	}
	setting, defined := r.environment["CLAUDE_CODE_USE_POWERSHELL_TOOL"]
	return ResolveAvailability(r.platform, r.GitBash(ctx), r.environment["USER_TYPE"], setting, defined)
}

type Snapshot struct {
	GitBash, PowerShell string
	Availability        Availability
}

// Platform is the immutable OS-discovery input. Runtime factories normally
// supply their actual host platform; parity fixtures may explicitly bind an
// OS observation while retaining a separate physical process boundary.
func (r *Resolver) Platform() string { return r.platform }

// Warm runs on the caller's native-worker lane before synchronous tool catalog
// assembly. Snapshot subsequently reads only this immutable cached result.
func (r *Resolver) Warm(ctx context.Context) Snapshot {
	bash, ps := "", ""
	if IsWindows(r.platform) {
		bash = r.GitBash(ctx)
	}
	ps = r.PowerShell(ctx)
	return Snapshot{GitBash: bash, PowerShell: ps, Availability: r.Availability(ctx)}
}

func (r *Resolver) Snapshot() (Snapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.psResolved || IsWindows(r.platform) && !r.bashResolved {
		return Snapshot{}, false
	}
	setting, defined := r.environment["CLAUDE_CODE_USE_POWERSHELL_TOOL"]
	return Snapshot{GitBash: r.bash, PowerShell: r.ps, Availability: ResolveAvailability(r.platform, r.bash, r.environment["USER_TYPE"], setting, defined)}, true
}

// GitBash preserves Source's authoritative explicit override, standard Git
// order and where.exe current-directory exclusion. Discovery is cached for one
// original CLI runtime incarnation, including an unavailable result.
func (r *Resolver) GitBash(ctx context.Context) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bashResolved {
		return r.bash
	}
	if ctx.Err() != nil {
		return ""
	}
	exists := r.dependencies.PathExists
	if exists == nil {
		return ""
	}
	r.bashResolved = true
	if override := r.environment["CLAUDE_CODE_GIT_BASH_PATH"]; override != "" {
		if exists(ctx, override) {
			r.bash = override
		}
		return r.bash
	}
	git := ""
	for _, candidate := range []string{`C:\Program Files\Git\cmd\git.exe`, `C:\Program Files (x86)\Git\cmd\git.exe`} {
		if exists(ctx, candidate) {
			git = candidate
			break
		}
	}
	if git == "" && r.dependencies.Where != nil {
		if data, err := r.dependencies.Where(ctx, "git"); err == nil {
			cwd := strings.ToLower(nativepaths.CleanWindows(r.cwd))
			for _, candidate := range strings.Split(protocol.TrimJavaScriptString(data), "\r\n") {
				if candidate == "" {
					continue
				}
				resolved := nativepaths.ResolveWindows(candidate, r.cwd)
				normalized := strings.ToLower(resolved)
				if strings.ToLower(nativepaths.DirWindows(normalized)) == cwd || strings.HasPrefix(normalized, cwd+`\`) {
					continue
				}
				git = candidate
				break
			}
		}
	}
	if git != "" {
		bash := nativepaths.JoinWindows(git, "..", "..", "bin", "bash.exe")
		if exists(ctx, bash) {
			r.bash = bash
		}
	}
	return r.bash
}

func (r *Resolver) GitBashUnavailableError() error {
	if override := r.environment["CLAUDE_CODE_GIT_BASH_PATH"]; override != "" {
		return fmt.Errorf("Claude Code was unable to find CLAUDE_CODE_GIT_BASH_PATH path %q", override)
	}
	return fmt.Errorf("Claude Code on Windows requires git-bash (https://git-scm.com/downloads/win). If installed but not in PATH, set environment variable pointing to your bash.exe, similar to: CLAUDE_CODE_GIT_BASH_PATH=C:\\Program Files\\Git\\bin\\bash.exe")
}

func PowerShellExecutable(candidate string) bool {
	parts := strings.FieldsFunc(protocol.TrimJavaScriptString(candidate), func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return false
	}
	name := strings.TrimSuffix(strings.ToLower(parts[len(parts)-1]), ".exe")
	return name == "pwsh" || name == "powershell"
}

func (r *Resolver) powerShellOverride(ctx context.Context, override string) string {
	override = protocol.TrimJavaScriptString(override)
	if !PowerShellExecutable(override) || r.dependencies.FileExists == nil {
		return ""
	}
	if r.dependencies.FileExists(ctx, override) {
		return override
	}
	base := strings.TrimSuffix(strings.ToLower(strings.ReplaceAll(override, "\\", "/")), ".exe")
	core := base == "pwsh" || strings.HasSuffix(base, "/pwsh")
	if IsWindows(r.platform) {
		if core && r.dependencies.ReadWindowsPwshHome != nil {
			if home := r.dependencies.ReadWindowsPwshHome(ctx); home != "" {
				candidate := nativepaths.JoinWindows(home, "pwsh.exe")
				if r.dependencies.FileExists(ctx, candidate) {
					return candidate
				}
			}
		}
		candidate := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
		if core {
			candidate = `C:\Program Files\PowerShell\7\pwsh.exe`
		}
		if r.dependencies.FileExists(ctx, candidate) {
			return candidate
		}
	}
	if !strings.ContainsAny(override, `/\`) && r.dependencies.Which != nil {
		return r.dependencies.Which(ctx, override)
	}
	return ""
}

func (r *Resolver) PowerShell(ctx context.Context) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.psResolved {
		return r.ps
	}
	if ctx.Err() != nil {
		return ""
	}
	r.psResolved = true
	r.ps = r.powerShellOverride(ctx, r.environment["CLAUDE_CODE_POWERSHELL_PATH"])
	if r.ps == "" && r.dependencies.Which != nil {
		r.ps = r.dependencies.Which(ctx, "pwsh")
		if r.ps == "" {
			r.ps = r.dependencies.Which(ctx, "powershell")
		}
	}
	return r.ps
}

func Edition(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) > 0 && strings.TrimSuffix(strings.ToLower(parts[len(parts)-1]), ".exe") == "pwsh" {
		return "core"
	}
	return "desktop"
}
