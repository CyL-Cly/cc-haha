package clilauncher

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

func trimJS(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == '\ufeff' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Zs, r)
	})
}
func trimEndJS(s string) string {
	return strings.TrimRightFunc(s, func(r rune) bool {
		return r == '\ufeff' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Zs, r)
	})
}
func windowsEntry(s, home string) string {
	s = trimJS(s)
	s = strings.Trim(s, `"`)
	for _, pattern := range []string{`(?i)%USERPROFILE%`, `(?i)%HOMEDRIVE%%HOMEPATH%`} {
		s = regexp.MustCompile(pattern).ReplaceAllStringFunc(s, func(string) string { return home })
	}
	return strings.ToLower(strings.TrimRight(strings.ReplaceAll(s, "/", `\`), `\`))
}
func PathPresent(value, target, platform, home string) bool {
	if value == "" {
		return false
	}
	delimiter := ":"
	if platform == "windows" {
		delimiter = ";"
		target = windowsEntry(target, home)
	} else {
		target, _ = filepath.Abs(target)
	}
	for _, entry := range strings.Split(value, delimiter) {
		if platform == "windows" {
			if windowsEntry(entry, home) == target {
				return true
			}
		} else {
			entry = trimJS(entry)
			if entry != "" {
				absolute, e := filepath.Abs(entry)
				if e == nil && absolute == target {
					return true
				}
			}
		}
	}
	return false
}
func ManagedPathBlock(shell, bin, home string) string {
	expr := bin
	a, _ := filepath.Abs(bin)
	b, _ := filepath.Abs(filepath.Join(home, ".local", "bin"))
	if a == b {
		expr = "$HOME/.local/bin"
	}
	if shell == "fish" {
		return pathStart + "\nif not contains \"" + expr + "\" $PATH\n  set -gx PATH \"" + expr + "\" $PATH\nend\n" + pathEnd
	}
	return pathStart + "\nexport PATH=\"" + expr + ":$PATH\"\n" + pathEnd
}
func UpsertPathBlock(existing, block string) string {
	pattern := regexp.MustCompile(regexp.QuoteMeta(pathStart) + `(?s:.*?)` + regexp.QuoteMeta(pathEnd) + `\n?`)
	next := trimEndJS(block) + "\n"
	if at := pattern.FindStringIndex(existing); at != nil {
		return existing[:at[0]] + next + existing[at[1]:]
	}
	old := trimEndJS(existing)
	if old == "" {
		return next
	}
	return old + "\n\n" + next
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func UnixWrapper(source, config string) string {
	export := ""
	if config != "" {
		export = "export CLAUDE_CONFIG_DIR=" + shellQuote(config) + "\n"
	}
	return "#!/usr/bin/env bash\nset -euo pipefail\n\nSIDECAR=" + shellQuote(source) + "\nAPP_ROOT=" + shellQuote(filepath.Dir(source)) + "\n" + export + `

if [[ ! -x "$SIDECAR" ]]; then
  echo "claude-haha launcher could not find bundled sidecar: $SIDECAR" >&2
  exit 127
fi

# Bun-compiled macOS sidecars can be suspended by job control while reading the
# controlling TTY directly. A nested PTY keeps the terminal foreground handoff
# stable for the interactive TUI; non-interactive commands keep direct exec
# semantics and exit codes.
if [[ "$(uname -s)" == "Darwin" && -t 0 && -t 1 && -x /usr/bin/script ]]; then
  exec /usr/bin/script -q /dev/null "$SIDECAR" cli --app-root "$APP_ROOT" "$@"
fi

exec "$SIDECAR" cli --app-root "$APP_ROOT" "$@"
`
}
func WindowsWrapper(source, config string) string {
	lines := []string{"@echo off", "setlocal", `set "SIDECAR=` + source + `"`, `set "APP_ROOT=` + filepath.Dir(source) + `"`}
	if config != "" {
		lines = append(lines, `set "CLAUDE_CONFIG_DIR=`+config+`"`)
	}
	lines = append(lines, `if not exist "%SIDECAR%" (`, `  echo claude-haha launcher could not find bundled sidecar: %SIDECAR% 1>&2`, `  exit /b 127`, `)`, `"%SIDECAR%" cli --app-root "%APP_ROOT%" %*`, `exit /b %ERRORLEVEL%`)
	return strings.Join(lines, "\r\n")
}
