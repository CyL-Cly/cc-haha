package builtintools

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func gitRuleWhitespace(r rune) bool {
	return r >= 9 && r <= 13 || r == 32 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}

// Source's security-review prelude consists of static Git inspection commands.
// This producer handles their exact/prefix rules without assuming a compound
// command is atomic. Other shell syntax still requires the original AST port.
func staticGitInspection(command string) bool {
	command = strings.TrimFunc(command, gitRuleWhitespace)
	if strings.ContainsAny(command, "\n\r\t;&|<>$`\\'\"(){}!#") {
		return false
	}
	words := strings.Fields(command)
	if len(words) < 2 || words[0] != "git" {
		return false
	}
	switch words[1] {
	case "status":
		return len(words) == 2
	case "diff":
		return len(words) == 3 && words[2] == "origin/HEAD..." || len(words) == 4 && words[2] == "--name-only" && words[3] == "origin/HEAD..."
	case "log":
		return len(words) == 4 && words[2] == "--no-decorate" && words[3] == "origin/HEAD..."
	default:
		return false
	}
}

func gitInspectionRuleMatches(rule, command string) (bool, error) {
	if rule == "Bash" || rule == "Bash(*)" {
		return true, nil
	}
	if !strings.HasPrefix(rule, "Bash(") {
		return false, nil
	}
	if !strings.HasSuffix(rule, ")") {
		return false, capability("Bash invalid command permission-rule consumer")
	}
	value := strings.TrimSuffix(strings.TrimPrefix(rule, "Bash("), ")")
	if prefix, ok := strings.CutSuffix(value, ":*"); ok && prefix != "" {
		return command == prefix || strings.HasPrefix(command, prefix+" ") || command == "xargs "+prefix || strings.HasPrefix(command, "xargs "+prefix+" "), nil
	}
	if strings.ContainsAny(value, "*\\") {
		return false, capability("Bash wildcard command permission-rule consumer")
	}
	return value == command, nil
}

func (s *Service) bashGitRuleDecision(context protocol.JSONObject, input protocol.JSONObject) (string, error) {
	command := strings.TrimFunc(input.String("command"), gitRuleWhitespace)
	if !staticGitInspection(command) {
		return "", capability("Bash AST command permission-rule consumer")
	}
	mode := context.String("mode")
	bypass := mode == "bypassPermissions" || mode == "plan" && string(context.Get("isBypassPermissionsModeAvailable")) == "true"
	for _, key := range []string{"alwaysDenyRules", "alwaysAskRules", "alwaysAllowRules"} {
		if key == "alwaysAskRules" && bypass {
			continue
		}
		for _, list := range rules(context, key) {
			for _, rule := range list {
				matched, err := gitInspectionRuleMatches(rule, command)
				if err != nil {
					return "", err
				}
				if matched {
					switch key {
					case "alwaysDenyRules":
						return "deny", nil
					case "alwaysAskRules":
						return "ask", nil
					case "alwaysAllowRules":
						return "allow", nil
					}
				}
			}
		}
	}
	return "", nil
}
