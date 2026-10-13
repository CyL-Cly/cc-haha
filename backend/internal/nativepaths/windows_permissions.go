package nativepaths

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/ignorepattern"
)

type PermissionPattern struct{ Pattern, Source string }

var permissionSources = []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"}

func WindowsPatterns(context protocol.JSONObject, key, tool string) ([]PermissionPattern, error) {
	values, _ := context.Object(key)
	out := []PermissionPattern{}
	indices := map[string]int{}
	for _, source := range permissionSources {
		var rules []json.RawMessage
		if value := values.Get(source); value != nil {
			if err := json.Unmarshal(value, &rules); err != nil {
				return nil, err
			}
		}
		for _, rule := range rules {
			parsed, err := sdkpermissions.ParsePermissionRuleValue(rule)
			if err != nil {
				return nil, err
			}
			if parsed.String("toolName") != tool || parsed.Get("ruleContent") == nil {
				continue
			}
			pattern := parsed.String("ruleContent")
			row := PermissionPattern{Pattern: pattern, Source: source}
			if index, exists := indices[pattern]; exists {
				out[index] = row
			} else {
				indices[pattern] = len(out)
				out = append(out, row)
			}
		}
	}
	return out, nil
}

// MatchWindowsPatterns is the actual Source patternWithRoot + ordered root /
// ignore pipeline. Inputs and matching remain lexical, even for UNC targets.
func MatchWindowsPatterns(value, cwd, home string, roots map[string]string, patterns []PermissionPattern) (bool, error) {
	matched, err := MatchingWindowsPattern(value, cwd, home, roots, patterns)
	return matched != nil, err
}

func MatchingWindowsPattern(value, cwd, home string, roots map[string]string, patterns []PermissionPattern) (*PermissionPattern, error) {
	type group struct {
		root     string
		patterns []string
		rules    map[string]PermissionPattern
	}
	groups := []group{}
	indices := map[string]int{}
	for _, row := range patterns {
		root, pattern := cwd, row.Pattern
		switch {
		case strings.HasPrefix(pattern, "//"):
			pattern = pattern[1:]
			if len(pattern) >= 3 && pattern[0] == '/' && driveLetter(pattern[1]) && pattern[2] == '/' {
				root = strings.ToUpper(pattern[1:2]) + `:\`
				pattern = pattern[3:]
			} else {
				root = "/"
			}
		case strings.HasPrefix(pattern, "~/"):
			root, pattern = home, pattern[1:]
		case strings.HasPrefix(pattern, "/"):
			root = roots[row.Source]
			if root == "" {
				return nil, errors.New("missing permission source root: " + row.Source)
			}
		default:
			pattern = strings.TrimPrefix(pattern, "./")
		}
		index, exists := indices[root]
		if !exists {
			index = len(groups)
			indices[root] = index
			groups = append(groups, group{root: root, rules: map[string]PermissionPattern{}})
		}
		if _, exists := groups[index].rules[pattern]; !exists {
			groups[index].patterns = append(groups[index].patterns, strings.TrimSuffix(pattern, "/**"))
		}
		groups[index].rules[pattern] = row
	}
	for _, g := range groups {
		relative := RelativeWindows(g.root, WindowsToPOSIX(value))
		if relative == "" || strings.HasPrefix(relative, "../") {
			continue
		}
		matcher, err := ignorepattern.New("windows")
		if err != nil {
			return nil, err
		}
		for _, pattern := range g.patterns {
			if err := matcher.Add(pattern); err != nil {
				return nil, err
			}
		}
		result, pattern, err := matcher.TestRule(relative)
		if err != nil {
			return nil, err
		}
		if result.Ignored {
			if rule, ok := g.rules[pattern+"/**"]; ok {
				return &rule, nil
			}
			if rule, ok := g.rules[pattern]; ok {
				return &rule, nil
			}
		}
	}
	return nil, nil
}
