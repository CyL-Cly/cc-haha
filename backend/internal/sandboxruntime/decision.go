package sandboxruntime

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

var sandboxEnvironmentPrefix = regexp.MustCompile("^([A-Za-z_][A-Za-z0-9_]*(?:\\[[^\\]]*\\])?)\\+?=(?:'[^'\\n\\r]*'|\"(?:\\\\.|[^\"$`\\\\\\n\\r])*\"|\\\\.|[^ \\t\\n\\r$`;|&()<>\\\\'\"])*[ \\t]+")
var sandboxWrappers = []*regexp.Regexp{
	regexp.MustCompile(`^timeout[ \t]+(?:(?:--(?:foreground|preserve-status|verbose)|--(?:kill-after|signal)=[A-Za-z0-9_.+-]+|--(?:kill-after|signal)[ \t]+[A-Za-z0-9_.+-]+|-v|-[ks][ \t]+[A-Za-z0-9_.+-]+|-[ks][A-Za-z0-9_.+-]+)[ \t]+)*(?:--[ \t]+)?\d+(?:\.\d+)?[smhd]?[ \t]+`),
	regexp.MustCompile(`^time[ \t]+(?:--[ \t]+)?`),
	regexp.MustCompile(`^nice(?:[ \t]+-n[ \t]+-?\d+|[ \t]+-\d+)?[ \t]+(?:--[ \t]+)?`),
	regexp.MustCompile(`^stdbuf(?:[ \t]+-[ioe][LN0-9]+)+[ \t]+(?:--[ \t]+)?`),
	regexp.MustCompile(`^nohup[ \t]+(?:--[ \t]+)?`),
}

func sandboxRuleHasWildcards(pattern string) bool {
	if strings.HasSuffix(pattern, ":*") {
		return false
	}
	for i, r := range pattern {
		if r == '*' {
			slashes := 0
			for j := i - 1; j >= 0 && pattern[j] == '\\'; j-- {
				slashes++
			}
			if slashes%2 == 0 {
				return true
			}
		}
	}
	return false
}

func stripSandboxComments(command string) string {
	kept := []string{}
	for _, line := range strings.Split(command, "\n") {
		trimmed := strings.TrimFunc(line, jsWhitespace)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return command
	}
	return strings.Join(kept, "\n")
}
func stripSandboxEnvironment(command string, blockHijack bool) string {
	for previous := ""; command != previous; {
		previous = command
		command = stripSandboxComments(command)
		m := sandboxEnvironmentPrefix.FindStringSubmatchIndex(command)
		if m != nil {
			name := command[m[2]:m[3]]
			if blockHijack && (strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") || name == "PATH") {
				break
			}
			command = command[m[1]:]
		}
	}
	return strings.TrimFunc(command, jsWhitespace)
}
func stripSandboxWrappers(command string) string {
	for previous := ""; command != previous; {
		previous = command
		command = stripSandboxComments(command)
		for _, pattern := range sandboxWrappers {
			command = pattern.ReplaceAllString(command, "")
		}
	}
	return strings.TrimFunc(command, jsWhitespace)
}

// ShouldUse follows Source's fixed-point excludedCommands candidates. This is
// the execution selector, not a Bash authorization grant or an AST security
// substitute. The shared original splitter and wildcard matcher remain owners
// of their respective parsing semantics.
func ShouldUse(settings protocol.JSONObject, enabled bool, command string, override bool) (bool, error) {
	if !enabled {
		return false, nil
	}
	sandbox := object(settings, "sandbox")
	if override && string(sandbox.Get("allowUnsandboxedCommands")) != "false" {
		return false, nil
	}
	if command == "" {
		return false, nil
	}
	excluded := texts(sandbox, "excludedCommands")
	if len(excluded) == 0 {
		return true, nil
	}
	classifier, e := sessionrewind.NewRecordedCommandClassifier("darwin", "external")
	if e != nil {
		return false, e
	}
	parts, e := classifier.SplitForExitCode(command)
	if e != nil {
		parts = []string{command}
	}
	for _, part := range parts {
		candidates := []string{strings.TrimFunc(part, jsWhitespace)}
		seen := map[string]bool{candidates[0]: true}
		for i := 0; i < len(candidates); i++ {
			for _, next := range []string{stripSandboxEnvironment(candidates[i], true), stripSandboxWrappers(candidates[i])} {
				if !seen[next] {
					seen[next] = true
					candidates = append(candidates, next)
				}
			}
		}
		for _, pattern := range excluded {
			for _, candidate := range candidates {
				if prefix, ok := strings.CutSuffix(pattern, ":*"); ok && prefix != "" && !strings.ContainsAny(prefix, "\n\r\u2028\u2029") {
					if candidate == prefix || strings.HasPrefix(candidate, prefix+" ") {
						return false, nil
					}
					continue
				}
				if sandboxRuleHasWildcards(pattern) {
					p, _ := json.Marshal(pattern)
					v, _ := json.Marshal(candidate)
					matched, e := sdkpermissions.MatchWildcardRule(p, v)
					if e != nil {
						return false, e
					}
					if matched {
						return false, nil
					}
				} else if pattern == candidate {
					return false, nil
				}
			}
		}
	}
	return true, nil
}

func RuleCandidates(command string) []string {
	candidates := []string{command}
	seen := map[string]bool{command: true}
	for i := 0; i < len(candidates); i++ {
		if candidates[i] == "" {
			continue
		}
		for _, next := range []string{stripSandboxEnvironment(candidates[i], false), stripSandboxWrappers(candidates[i])} {
			if !seen[next] {
				seen[next] = true
				candidates = append(candidates, next)
			}
		}
	}
	return candidates
}
func MatchCommandRule(pattern, candidate string) (bool, error) {
	if prefix, ok := strings.CutSuffix(pattern, ":*"); ok && prefix != "" && !strings.ContainsAny(prefix, "\n\r\u2028\u2029") {
		return candidate == prefix || strings.HasPrefix(candidate, prefix+" ") || candidate == "xargs "+prefix || strings.HasPrefix(candidate, "xargs "+prefix+" "), nil
	}
	if sandboxRuleHasWildcards(pattern) {
		p, _ := json.Marshal(pattern)
		v, _ := json.Marshal(candidate)
		return sdkpermissions.MatchWildcardRule(p, v)
	}
	return pattern == candidate, nil
}

type AvailabilityInput struct {
	Settings, InitialSettings protocol.JSONObject
	Platform                  string
	Supported                 bool
	DependencyErrors          []string
}
type Availability struct {
	Enabled, Required bool
	Reason            string
}

func ResolveAvailability(in AvailabilityInput) Availability {
	sandbox := object(in.Settings, "sandbox")
	out := Availability{Required: truth(sandbox, "enabled") && truth(sandbox, "failIfUnavailable")}
	if !truth(sandbox, "enabled") {
		return out
	}
	if !in.Supported {
		if in.Platform == "wsl" {
			out.Reason = "sandbox.enabled is set but WSL1 is not supported (requires WSL2)"
		} else {
			out.Reason = "sandbox.enabled is set but " + in.Platform + " is not supported (requires macOS, Linux, or WSL2)"
		}
		return out
	}
	platforms := object(in.InitialSettings, "sandbox").Get("enabledPlatforms")
	if platforms != nil {
		found := true
		switch {
		case len(platforms) > 0 && platforms[0] == '[':
			var values []json.RawMessage
			_ = json.Unmarshal(platforms, &values)
			found = false
			for _, value := range values {
				var text string
				if json.Unmarshal(value, &text) == nil && text == in.Platform {
					found = true
				}
			}
		case len(platforms) > 0 && platforms[0] == '"':
			var text string
			_ = json.Unmarshal(platforms, &text)
			found = text != "" && strings.Contains(text, in.Platform)
		case len(platforms) > 0 && platforms[0] == '{':
			var value protocol.JSONObject
			_ = value.UnmarshalJSON(platforms)
			if string(value.Get("length")) == "0" {
				found = false
			}
		}
		// Other passthrough shapes throw in Source's .includes lookup and
		// its local catch defaults to enabled; do not reinterpret as [].
		if !found {
			out.Reason = "sandbox.enabled is set but " + in.Platform + " is not in sandbox.enabledPlatforms"
			return out
		}
	}
	if len(in.DependencyErrors) > 0 {
		hint := "run /sandbox or /doctor for details"
		if in.Platform != "macos" {
			hint = "install missing tools (e.g. apt install bubblewrap socat) or run /sandbox for details"
		}
		out.Reason = "sandbox.enabled is set but dependencies are missing: " + strings.Join(in.DependencyErrors, ", ") + " · " + hint
		return out
	}
	out.Enabled = true
	return out
}
