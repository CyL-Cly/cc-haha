package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/ignorepattern"
	"path/filepath"
	"strings"
)

// This is matchingRuleForInput's original ordered Map/root/ignore pipeline.
// The pinned ignore primitive performs only generic pattern-language matching.
func (s *Service) matchingHookFileRule(scope *cooperative.Scope, c protocol.JSONObject, name, behavior, path string) (*hookRule, error) {
	rows, e := orderedHookRules(c, behavior)
	if e != nil {
		return nil, e
	}
	unique := []hookRule{}
	indices := map[string]int{}
	for _, r := range rows {
		if r.value.String("toolName") != name || r.value.Get("ruleContent") == nil {
			continue
		}
		key := r.value.String("ruleContent")
		if i, ok := indices[key]; ok {
			unique[i] = r
		} else {
			indices[key] = len(unique)
			unique = append(unique, r)
		}
	}
	type group struct {
		root     string
		patterns []string
		rules    map[string]hookRule
	}
	groups := []group{}
	byRoot := map[string]int{}
	for _, r := range unique {
		pattern := r.value.String("ruleContent")
		root, key := s.options.CWD, "\x00"
		switch {
		case strings.HasPrefix(pattern, "//"):
			root = "/"
			pattern = pattern[1:]
			key = root
		case strings.HasPrefix(pattern, "~/"):
			root = s.options.Home
			pattern = pattern[1:]
			key = root
		case strings.HasPrefix(pattern, "/"):
			if s.options.HookPermissionRoot == nil {
				return nil, capability("actual Hook permission source root")
			}
			root, e = s.options.HookPermissionRoot(scope, r.source)
			if e != nil {
				return nil, e
			}
			key = root
		default:
			pattern = strings.TrimPrefix(pattern, "./")
		}
		i, ok := byRoot[key]
		if !ok {
			i = len(groups)
			byRoot[key] = i
			groups = append(groups, group{root: root, rules: map[string]hookRule{}})
		}
		if _, exists := groups[i].rules[pattern]; !exists {
			groups[i].patterns = append(groups[i].patterns, pattern)
		}
		groups[i].rules[pattern] = r
	}
	for _, g := range groups {
		relative, e := filepath.Rel(g.root, path)
		if e != nil {
			return nil, e
		}
		relative = filepath.ToSlash(relative)
		if strings.HasPrefix(relative, "../") || relative == "." || relative == "" {
			continue
		}
		matcher, e := ignorepattern.New(s.options.Platform)
		if e != nil {
			return nil, e
		}
		for _, pattern := range g.patterns {
			if e = matcher.Add(strings.TrimSuffix(pattern, "/**")); e != nil {
				return nil, e
			}
		}
		result, pattern, e := matcher.TestRule(relative)
		if e != nil {
			return nil, e
		}
		if !result.Ignored || pattern == "" {
			continue
		}
		if rule, ok := g.rules[pattern+"/**"]; ok {
			return &rule, nil
		}
		if rule, ok := g.rules[pattern]; ok {
			return &rule, nil
		}
	}
	return nil, nil
}
