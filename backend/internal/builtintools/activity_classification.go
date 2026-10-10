package builtintools

import (
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

var activityWhitespace = regexp.MustCompile(`[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)

func activityFirstWord(part string) string {
	part = protocol.TrimJavaScriptString(part)
	return activityWhitespace.Split(part, 2)[0]
}
func activityMember(name string, values ...string) bool {
	for _, value := range values {
		if name == value {
			return true
		}
	}
	return false
}

// NativeActivityClassification ports the isSearch/isRead projections used by
// LocalAgentTask. This is display metadata, never an execution permission.
// Source's fallback primitive catalogue means Read/Grep/Glob remain available
// to virtual tool messages even if the parent execution catalogue omits them.
func NativeActivityClassification(name string, in protocol.JSONObject) (isSearch, isRead bool, failure error) {
	switch name {
	case "Read":
		return false, true, nil
	case "Grep", "Glob":
		return true, false, nil
	case "Bash":
		definition, err := toolschema.NativeBuiltinDefinition("Bash")
		if err != nil {
			return false, false, err
		}
		parsed, err := toolschema.ParseInput(definition, in)
		if err != nil {
			return false, false, nil
		}
		classifier, err := sessionrewind.NewRecordedCommandClassifier("darwin", "")
		if err != nil {
			return false, false, err
		}
		parts, err := classifier.SplitWithOperators(parsed.String("command"))
		if err != nil {
			return false, false, nil
		}
		skip, nonNeutral := false, false
		for _, part := range parts {
			if skip {
				skip = false
				continue
			}
			if activityMember(part, ">", ">>", ">&") {
				skip = true
				continue
			}
			if activityMember(part, "||", "&&", "|", ";") {
				continue
			}
			base := activityFirstWord(part)
			if base == "" || activityMember(base, "echo", "printf", "true", "false", ":") {
				continue
			}
			nonNeutral = true
			search := activityMember(base, "find", "grep", "rg", "ag", "ack", "locate", "which", "whereis")
			read := activityMember(base, "cat", "head", "tail", "less", "more", "wc", "stat", "file", "strings", "jq", "awk", "cut", "sort", "uniq", "tr")
			list := activityMember(base, "ls", "tree", "du")
			if !search && !read && !list {
				return false, false, nil
			}
			isSearch = isSearch || search
			isRead = isRead || read
		}
		if !nonNeutral {
			return false, false, nil
		}
		return isSearch, isRead, nil
	case "PowerShell":
		command := protocol.TrimJavaScriptString(in.String("command"))
		if command == "" {
			return false, false, nil
		}
		nonNeutral := false
		for _, part := range strings.FieldsFunc(command, func(r rune) bool { return r == ';' || r == '|' }) {
			base := strings.ToLower(activityFirstWord(part))
			if base == "" {
				continue
			}
			if !strings.ContainsAny(base, `\/`) {
				for _, extension := range []string{".exe", ".cmd", ".bat", ".com"} {
					if strings.HasSuffix(base, extension) {
						base = strings.TrimSuffix(base, extension)
						break
					}
				}
			}
			switch base {
			case "ls", "dir", "gci":
				base = "get-childitem"
			case "cat", "type", "gc":
				base = "get-content"
			case "pwd", "gl":
				base = "get-location"
			case "gi":
				base = "get-item"
			case "ps", "gps":
				base = "get-process"
			case "gsv":
				base = "get-service"
			case "echo", "write":
				base = "write-output"
			case "sls":
				base = "select-string"
			}
			if activityMember(base, "write-output", "write-host") {
				continue
			}
			nonNeutral = true
			search := activityMember(base, "select-string", "get-childitem", "findstr", "where.exe")
			read := activityMember(base, "get-content", "get-item", "test-path", "resolve-path", "get-process", "get-service", "get-childitem", "get-location", "get-filehash", "get-acl", "format-hex")
			if !search && !read {
				return false, false, nil
			}
			isSearch = isSearch || search
			isRead = isRead || read
		}
		if !nonNeutral {
			return false, false, nil
		}
		return isSearch, isRead, nil
	}
	return false, false, nil
}
