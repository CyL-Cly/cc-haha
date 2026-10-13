package powershellpolicy

import (
	"path"
	"regexp"
	"strings"
)

var filesystemProvider = regexp.MustCompile(`^(?:[A-Z0-9_.]+\\){0,3}FILESYSTEM::`)
var shortGitDirectory = regexp.MustCompile(`^git~\d+(?:$|/)`)

func normalizeGitArgument(value string) string {
	if IsParameter(value, "") || strings.HasPrefix(value, "/") {
		if colon := strings.Index(value[minimum(1, len(value)):], ":"); colon >= 0 {
			value = value[colon+2:]
		}
	}
	value = stripQuotes(value)
	value = strings.ReplaceAll(value, "`", "")
	if match := filesystemProvider.FindStringIndex(ecmaRegexFold(value)); match != nil {
		value = value[match[1]:]
	}
	if len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (len(value) == 2 || value[2] != '/' && value[2] != '\\') {
		value = value[2:]
	}
	value = strings.ReplaceAll(value, `\`, "/")
	parts := strings.Split(value, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		for {
			previous := part
			part = strings.TrimRight(part, " ")
			if part == "." || part == ".." {
				break
			}
			part = strings.TrimRight(part, ".")
			if part == previous {
				break
			}
		}
		if part == "" {
			part = "."
		}
		parts[i] = part
	}
	return jsLower(path.Clean(strings.Join(parts, "/")))
}
func gitPrefix(value string, dotOnly bool) bool {
	if value == ".git" || strings.HasPrefix(value, ".git/") || shortGitDirectory.MatchString(value) {
		return true
	}
	if dotOnly {
		return false
	}
	if value == "head" {
		return true
	}
	for _, prefix := range []string{"objects", "refs", "hooks"} {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return true
		}
	}
	return false
}
func (c PathContext) gitPath(value string, dotOnly bool) bool {
	normalized := normalizeGitArgument(value)
	cwdBase := jsLower(path.Base(strings.ReplaceAll(c.CWD, `\`, "/")))
	prefix := "../" + cwdBase + "/"
	for strings.HasPrefix(normalized, prefix) {
		normalized = strings.TrimPrefix(normalized, prefix)
	}
	if normalized == "../"+cwdBase {
		normalized = "."
	}
	if gitPrefix(normalized, dotOnly) {
		return true
	}
	if strings.HasPrefix(normalized, "../") || strings.HasPrefix(normalized, "/") || driveName.MatchString(normalized) {
		absolute := jsLower(strings.ReplaceAll(c.absolute(normalized), `\`, "/"))
		cwd := jsLower(strings.ReplaceAll(c.CWD, `\`, "/"))
		if absolute == cwd {
			return gitPrefix(".", dotOnly)
		}
		if strings.HasPrefix(absolute, strings.TrimSuffix(cwd, "/")+"/") {
			return gitPrefix(absolute[len(strings.TrimSuffix(cwd, "/"))+1:], dotOnly)
		}
	}
	return false
}
