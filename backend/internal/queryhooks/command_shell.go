package queryhooks

import (
	"github.com/dlclark/regexp2/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type CommandShellResolver struct {
	environment map[string]string
	platform    string
	once        sync.Once
	path        string
}

func NewCommandShellResolver(environment map[string]string, platform string) *CommandShellResolver {
	copy := map[string]string{}
	for k, v := range environment {
		copy[k] = v
	}
	return &CommandShellResolver{environment: copy, platform: platform}
}
func shellFile(path string) bool {
	info, e := os.Stat(path)
	return e == nil && info.Mode().IsRegular()
}
func (r *CommandShellResolver) which(name string) string {
	for _, dir := range filepath.SplitList(r.environment["PATH"]) {
		if dir == "" {
			dir = "."
		}
		path := filepath.Join(dir, name)
		info, e := os.Stat(path)
		if e == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			absolute, e := filepath.Abs(path)
			if e == nil {
				return absolute
			}
		}
	}
	return ""
}
func (r *CommandShellResolver) PowerShell() string {
	r.once.Do(func() {
		override := strings.TrimFunc(r.environment["CLAUDE_CODE_POWERSHELL_PATH"], javaScriptWhitespace)
		parts := strings.FieldsFunc(strings.ToLower(override), func(c rune) bool { return c == '/' || c == '\\' })
		name := ""
		if len(parts) > 0 {
			name = strings.TrimSuffix(parts[len(parts)-1], ".exe")
		}
		if name == "pwsh" || name == "powershell" {
			if shellFile(override) {
				r.path = override
				return
			}
			if !strings.ContainsAny(override, "/\\") {
				if resolved := r.which(override); resolved != "" {
					r.path = resolved
					return
				}
			}
		}
		if path := r.which("pwsh"); path != "" {
			if r.platform == "linux" {
				real, e := filepath.EvalSymlinks(path)
				if e != nil {
					real = path
				}
				if strings.HasPrefix(path, "/snap/") || strings.HasPrefix(real, "/snap/") {
					for _, direct := range []string{"/opt/microsoft/powershell/7/pwsh", "/usr/bin/pwsh"} {
						if !shellFile(direct) {
							continue
						}
						resolved, e := filepath.EvalSymlinks(direct)
						if e != nil {
							resolved = direct
						}
						if !strings.HasPrefix(direct, "/snap/") && !strings.HasPrefix(resolved, "/snap/") {
							r.path = direct
							return
						}
					}
				}
			}
			r.path = path
			return
		}
		r.path = r.which("powershell")
	})
	return r.path
}

func quoteHookShell(value string) (string, error) {
	if value == "" {
		return "''", nil
	}
	space := regexp2.MustCompile(`["\s\\]`, regexp2.ECMAScript)
	match, e := space.MatchString(value)
	if e != nil {
		return "", e
	}
	if match && !strings.Contains(value, "'") {
		return "'" + value + "'", nil
	}
	space = regexp2.MustCompile(`["'\s]`, regexp2.ECMAScript)
	match, e = space.MatchString(value)
	if e != nil {
		return "", e
	}
	if match {
		var out strings.Builder
		out.WriteByte('"')
		for _, r := range value {
			if strings.ContainsRune("\"\\$`!", r) {
				out.WriteByte('\\')
			}
			out.WriteRune(r)
		}
		out.WriteByte('"')
		return out.String(), nil
	}
	pattern := regexp2.MustCompile(`([A-Za-z]:)?([#!"$&'()*,:;<=>?@[\\\]^`+"`"+`{|}])`, regexp2.ECMAScript)
	return pattern.ReplaceFunc(value, func(m regexp2.Match) string { return m.Groups()[1].String() + "\\" + m.Groups()[2].String() }, -1, -1)
}
func FormatHookShellPrefix(prefix, command string) (string, error) {
	index := strings.LastIndex(prefix, " -")
	path, args := prefix, ""
	if index > 0 {
		path, args = prefix[:index], prefix[index+1:]
	}
	quoted, e := quoteHookShell(path)
	if e != nil {
		return "", e
	}
	body, e := quoteHookShell(command)
	if e != nil {
		return "", e
	}
	if args != "" {
		return quoted + " " + args + " " + body, nil
	}
	return quoted + " " + body, nil
}
