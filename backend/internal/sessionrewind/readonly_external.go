package sessionrewind

import "strings"

// ExternalReadOnlyArgs validates already parsed native argv against the same
// Source flag/callback configurations shared by Bash and PowerShell. It does
// not tokenize PowerShell with shell-quote or run Bash's expansion heuristics.
func (c *RecordedCommandClassifier) ExternalReadOnlyArgs(key string, args []string) (bool, bool, error) {
	policies := readonlyCompile.policy.Commands
	if c.UserType == "ant" {
		policies = append(append([]commandPolicy{}, policies...), readonlyCompile.policy.Ant...)
	}
	for _, policy := range policies {
		if policy.Name != key {
			continue
		}
		parts := strings.Split(key, " ")
		// Native validateFlags receives commandName=git in Source so numeric
		// shorthand and negative --sort values keep the same semantics.
		tokens := append(parts, args...)
		if policy.Callback {
			danger, err := c.policyDangerous(key, "", args)
			if err != nil || danger {
				return false, true, err
			}
		}
		return validateNativeFlags(tokens, len(parts), policy), true, nil
	}
	return false, false, nil
}
