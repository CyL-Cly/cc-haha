package builtintools

import (
	"github.com/dlclark/regexp2/v2"
	"github.com/dop251/goja"
	"path/filepath"
	"strings"
)

type HookPathSafetyContext struct {
	CWD, Platform string
	SettingsPaths []string
}
type HookPathSafetyResult struct {
	Safe                 bool   `json:"safe"`
	Message              string `json:"message,omitempty"`
	ClassifierApprovable *bool  `json:"classifierApprovable,omitempty"`
}

func hookSafetyMatch(pattern, value string) bool {
	re, e := regexp2.Compile(pattern, regexp2.ECMAScript)
	if e != nil {
		return false
	}
	matched, e := re.MatchString(value)
	return e == nil && matched
}
func suspiciousHookPath(path, platform string, allowUNC bool) bool {
	if (platform == "windows" || platform == "wsl") && len(path) > 2 && strings.Contains(path[2:], ":") {
		return true
	}
	if hookSafetyMatch(`~\d`, path) || strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) || strings.HasPrefix(path, "//?/") || strings.HasPrefix(path, "//./") || hookSafetyMatch(`[.\s]+$`, path) || hookSafetyMatch(`\.(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`, strings.ToUpper(path)) || hookSafetyMatch(`(^|/|\\)\.{3,}(/|\\|$)`, path) {
		return true
	}
	if platform == "windows" && !allowUNC {
		for _, pattern := range []string{`\\\\[^\s\\/]+(?:@(?:\d+|ssl))?(?:[\\/]|$|\s)`, `(?<!:)//[^\s\\/]+(?:@(?:\d+|ssl))?(?:[\\/]|$|\s)`, `/\\{2,}[^\s\\/]`, `\\{2,}/[^\s\\/]`, `@SSL@\d+`, `@\d+@SSL`, `DavWWWRoot`} {
			if hookSafetyMatch("(?i)"+pattern, path) {
				return true
			}
		}
	}
	return false
}
func hookExpandedSafetyPath(path string, c HookPathSafetyContext) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.CWD, path)
	}
	return filepath.Clean(path)
}
func hookCasePath(path string) string {
	vm := goja.New()
	f, _ := goja.AssertFunction(vm.Get("String").ToObject(vm).Get("prototype").ToObject(vm).Get("toLowerCase"))
	v, e := f(vm.ToValue(path))
	if e != nil {
		panic(e)
	}
	return v.String()
}
func hookSafetyUnder(path, root string) bool {
	normalize := func(p string) string {
		p = filepath.Clean(p)
		if strings.HasPrefix(p, "/private/var/") {
			p = strings.TrimPrefix(p, "/private")
		}
		if p == "/private/tmp" || strings.HasPrefix(p, "/private/tmp/") {
			p = strings.TrimPrefix(p, "/private")
		}
		return hookCasePath(p)
	}
	p, r := normalize(path), normalize(root)
	return p == r || strings.HasPrefix(p, r+string(filepath.Separator))
}
func claudeHookConfigPath(path string, c HookPathSafetyContext) bool {
	p := hookCasePath(hookExpandedSafetyPath(path, c))
	sep := string(filepath.Separator)
	if strings.HasSuffix(p, sep+".claude"+sep+"settings.json") || strings.HasSuffix(p, sep+".claude"+sep+"settings.local.json") {
		return true
	}
	for _, configured := range c.SettingsPaths {
		if hookCasePath(configured) == p {
			return true
		}
	}
	for _, dir := range []string{"commands", "agents", "skills"} {
		if hookSafetyUnder(p, filepath.Join(c.CWD, ".claude", dir)) {
			return true
		}
	}
	return false
}
func dangerousHookPath(path string, c HookPathSafetyContext, allowUNC bool) bool {
	if !allowUNC && (strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//")) {
		return true
	}
	parts := strings.Split(hookExpandedSafetyPath(path, c), string(filepath.Separator))
	for i, part := range parts {
		lower := hookCasePath(part)
		for _, dir := range []string{".git", ".vscode", ".idea", ".claude"} {
			if lower != dir {
				continue
			}
			if dir == ".claude" && i+1 < len(parts) && hookCasePath(parts[i+1]) == "worktrees" {
				break
			}
			return true
		}
	}
	file := hookCasePath(parts[len(parts)-1])
	for _, name := range []string{".gitconfig", ".gitmodules", ".bashrc", ".bash_profile", ".zshrc", ".zprofile", ".profile", ".ripgreprc", ".mcp.json", ".claude.json"} {
		if file == name {
			return true
		}
	}
	return false
}

// CheckHookPathSafety is Source checkPathSafetyForAutoEdit's three passes.
// Canonical forms/settings paths are supplied by actual native producers.
func CheckHookPathSafety(path string, forms []string, c HookPathSafetyContext, allowUNC bool) HookPathSafetyResult {
	unsafe := func(message string, approvable bool) HookPathSafetyResult {
		return HookPathSafetyResult{Message: message, ClassifierApprovable: &approvable}
	}
	for _, form := range forms {
		if suspiciousHookPath(form, c.Platform, allowUNC) {
			return unsafe("Claude requested permissions to write to "+path+", which contains a suspicious Windows path pattern that requires manual approval.", false)
		}
	}
	for _, form := range forms {
		if claudeHookConfigPath(form, c) {
			return unsafe("Claude requested permissions to write to "+path+", but you haven't granted it yet.", true)
		}
	}
	for _, form := range forms {
		if dangerousHookPath(form, c, allowUNC) {
			return unsafe("Claude requested permissions to edit "+path+" which is a sensitive file.", true)
		}
	}
	return HookPathSafetyResult{Safe: true}
}
