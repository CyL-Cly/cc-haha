package builtintools

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func normalizedWorkingPath(value string) string {
	value = filepath.Clean(value)
	if strings.HasPrefix(value, "/private/var/") {
		value = strings.TrimPrefix(value, "/private")
	}
	if value == "/private/tmp" || strings.HasPrefix(value, "/private/tmp/") {
		value = strings.TrimPrefix(value, "/private")
	}
	// Source normalizes case on every platform and uses contextual Unicode
	// lowercase (including final sigma and dotted capital I).
	return cases.Lower(language.Und).String(value)
}
func pathInWorkingPath(path, root string) bool {
	rel, e := filepath.Rel(normalizedWorkingPath(root), normalizedWorkingPath(path))
	if e != nil {
		return false
	}
	if rel == "." || rel == "" {
		return true
	}
	if regexpTraversal.MatchString(rel) {
		return false
	}
	return !filepath.IsAbs(rel)
}

var regexpTraversal = regexp.MustCompile(`(?:^|[\\/])\.\.(?:[\\/]|$)`)

func (s *Service) pathInAllowedWorkingPath(c protocol.JSONObject, path string) bool {
	roots := []string{s.options.CWD}
	var dirs protocol.JSONObject
	if dirs.UnmarshalJSON(c.Get("additionalWorkingDirectories")) == nil {
		var entries [][]json.RawMessage
		if json.Unmarshal(dirs.Get("map"), &entries) == nil {
			for _, entry := range entries {
				if len(entry) == 0 {
					continue
				}
				var root string
				if json.Unmarshal(entry[0], &root) == nil {
					roots = append(roots, root)
				}
			}
		}
	}
	working := []string{}
	for _, root := range roots {
		working = append(working, root, s.permissionResolved(root))
	}
	// Every lexical/canonical target must be within some lexical/canonical
	// working root. Symlinks escaping the whole root set still require approval.
	for _, target := range []string{path, s.permissionResolved(path)} {
		allowed := false
		for _, root := range working {
			if s.workingPathContains(target, root) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}
