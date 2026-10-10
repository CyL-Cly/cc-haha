package sandboxruntime

import (
	"path/filepath"
	"regexp"
	"strings"
)

type PathContext struct {
	Home, CWD string
	Realpath  func(string) (string, error)
}

// Source process.cwd() reports the physical directory after chdir, even when
// the caller passed a /var alias or a project symlink. That precedes the
// separate boundary check for configured path patterns.
func resolvedWorkingDirectory(ctx PathContext) string {
	if ctx.Realpath != nil {
		if cwd, err := ctx.Realpath(ctx.CWD); err == nil {
			return cwd
		}
	}
	return ctx.CWD
}

func ContainsGlob(path string) bool { return strings.ContainsAny(path, "*?[]") }
func StripGlobSuffix(path string) string {
	path = strings.TrimSuffix(path, "/**")
	if path == "" {
		return "/"
	}
	return path
}
func SymlinkOutsideBoundary(original, resolved string) bool {
	// Node path.normalize preserves a supplied trailing slash. It participates
	// in the original boundary comparison before realpath's result is accepted.
	normalize := func(value string) string {
		clean := filepath.Clean(value)
		if strings.HasSuffix(value, "/") && clean != "/" {
			clean += "/"
		}
		return clean
	}
	original = normalize(original)
	resolved = normalize(resolved)
	if original == resolved {
		return false
	}
	if (strings.HasPrefix(original, "/tmp/") || strings.HasPrefix(original, "/var/")) && resolved == "/private"+original {
		return false
	}
	if resolved == "/" || len(strings.Split(strings.Trim(resolved, "/"), "/")) <= 1 || strings.HasPrefix(original, resolved+"/") {
		return true
	}
	canonical := original
	if strings.HasPrefix(original, "/tmp/") || strings.HasPrefix(original, "/var/") {
		canonical = "/private" + original
	}
	if canonical != original && strings.HasPrefix(canonical, resolved+"/") {
		return true
	}
	return !(strings.HasPrefix(resolved, original+"/") || canonical != original && (resolved == canonical || strings.HasPrefix(resolved, canonical+"/")))
}
func NormalizePath(pattern string, ctx PathContext) string {
	value := pattern
	if value == "~" {
		value = ctx.Home
	} else if strings.HasPrefix(value, "~/") {
		value = ctx.Home + value[1:]
	} else if !filepath.IsAbs(value) {
		value = filepath.Join(resolvedWorkingDirectory(ctx), value)
	}
	if ctx.Realpath == nil {
		return value
	}
	if ContainsGlob(value) {
		index := strings.IndexAny(value, "*?[]")
		prefix := value[:index]
		if prefix != "" && prefix != "/" {
			base := filepath.Dir(prefix)
			if strings.HasSuffix(prefix, "/") {
				base = strings.TrimSuffix(prefix, "/")
			}
			if resolved, e := ctx.Realpath(base); e == nil && !SymlinkOutsideBoundary(base, resolved) {
				return resolved + value[len(base):]
			}
		}
		return value
	}
	if resolved, e := ctx.Realpath(value); e == nil && !SymlinkOutsideBoundary(value, resolved) {
		return resolved
	}
	return value
}

var regexSpecial = regexp.MustCompile(`[.^$+{}()|\\]`)
var unclosedBracket = regexp.MustCompile(`\[([^\]]*?)$`)

func GlobRegex(pattern string) string {
	pattern = regexSpecial.ReplaceAllStringFunc(pattern, func(v string) string { return "\\" + v })
	pattern = unclosedBracket.ReplaceAllString(pattern, `\[$1`)
	pattern = strings.ReplaceAll(pattern, "**/", "__GLOBSTAR_SLASH__")
	pattern = strings.ReplaceAll(pattern, "**", "__GLOBSTAR__")
	pattern = strings.ReplaceAll(pattern, "*", "[^/]*")
	pattern = strings.ReplaceAll(pattern, "?", "[^/]")
	pattern = strings.ReplaceAll(pattern, "__GLOBSTAR_SLASH__", "(.*/)?")
	pattern = strings.ReplaceAll(pattern, "__GLOBSTAR__", ".*")
	return "^" + pattern + "$"
}
func DefaultWritePaths(home string) []string {
	return []string{"/dev/stdout", "/dev/stderr", "/dev/null", "/dev/tty", "/dev/dtracehelper", "/dev/autofs_nowait", "/tmp/claude", "/private/tmp/claude", filepath.Join(home, ".npm/_logs"), filepath.Join(home, ".claude/debug")}
}

var dangerousFiles = []string{".gitconfig", ".gitmodules", ".bashrc", ".bash_profile", ".zshrc", ".zprofile", ".profile", ".ripgreprc", ".mcp.json"}
var dangerousDirectories = []string{".vscode", ".idea", ".claude/commands", ".claude/agents"}

func MandatoryMacDenyPaths(cwd string, allowGitConfig bool) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	for _, name := range dangerousFiles {
		add(filepath.Join(cwd, name))
		add("**/" + name)
	}
	for _, name := range dangerousDirectories {
		add(filepath.Join(cwd, name))
		add("**/" + name + "/**")
	}
	add(filepath.Join(cwd, ".git/hooks"))
	add("**/.git/hooks/**")
	if !allowGitConfig {
		add(filepath.Join(cwd, ".git/config"))
		add("**/.git/config")
	}
	return out
}
