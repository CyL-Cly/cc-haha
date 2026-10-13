package builtinwrite

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
	"io/fs"
	"path/filepath"
	"strings"
)

func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0xfeff, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// posixNormalize retains the trailing slash which node:path.normalize keeps.
func posixNormalize(path string) string {
	p := filepath.Clean(path)
	if strings.HasSuffix(path, "/") && p != "/" {
		p += "/"
	}
	return p
}
func (s *Service) expand(input json.RawMessage) (string, error) {
	path, e := scalar(input)
	if e != nil {
		return "", e
	}
	if s.options.Host.Platform == "windows" {
		return nativepaths.ExpandWindows(path, s.options.CWD, s.options.Home)
	}
	if strings.ContainsRune(path, 0) || strings.ContainsRune(s.options.CWD, 0) {
		return "", errors.New("Path contains null bytes")
	}
	path = strings.TrimFunc(path, jsSpace)
	switch {
	case path == "":
		path = posixNormalize(s.options.CWD)
	case path == "~":
		path = s.options.Home
	case strings.HasPrefix(path, "~/"):
		rest := path[2:]
		path = filepath.Join(s.options.Home, rest)
		if rest != "" && strings.HasSuffix(rest, "/") && path != "/" {
			path += "/"
		}
	case filepath.IsAbs(path):
		path = posixNormalize(path)
	default:
		path = filepath.Join(s.options.CWD, path)
	}
	return norm.NFC.String(path), nil
}
func unc(input json.RawMessage) bool {
	value, _ := scalar(input)
	return strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//")
}
func (s *Service) Backfill(input *protocol.JSONObject) error {
	if input == nil {
		return cooperative.ErrRestricted
	}
	path, e := s.expand(input.Get("file_path"))
	if e != nil {
		return e
	}
	input.Set("file_path", quoted(path))
	return nil
}

// The resolved Source context is read on the original Owner on every decision.
// Unsupported ignore patterns and missing settings roots cannot become allows.
func (s *Service) rules(scope *cooperative.Scope, context protocol.JSONObject, path string, wholeOnly bool) (wholeDeny, editDeny bool, err error) {
	if s.options.Host.Platform == "windows" {
		return s.windowsDenyRules(context, path, wholeOnly)
	}
	var bySource map[string][]string
	if e := json.Unmarshal(context.Get("alwaysDenyRules"), &bySource); e != nil {
		return false, false, capability("actual alwaysDenyRules source map")
	}
	for source, rules := range bySource {
		for _, rule := range rules {
			if rule == "Write" {
				wholeDeny = true
				continue
			}
			if wholeOnly {
				continue
			}
			// Other tools cannot veto this finite Write path. Write-specific content
			// rules require their actual parser; path-content rules are named Edit.
			if strings.HasPrefix(rule, "Write(") {
				return false, false, capability("Write content-specific permission rule")
			}
			if !strings.HasPrefix(rule, "Edit(") {
				continue
			}
			if !strings.HasSuffix(rule, ")") {
				return false, false, capability("permission rule value parser")
			}
			pattern := strings.TrimSuffix(strings.TrimPrefix(rule, "Edit("), ")")
			pattern = strings.TrimSuffix(pattern, "/**")
			if strings.ContainsAny(pattern, "*?[]!\\") || pattern == "" {
				return false, false, capability("Edit ignore-pattern permission matcher")
			}
			var root string
			anchored := false
			switch {
			case strings.HasPrefix(pattern, "//"):
				root = "/"
				anchored = true
				pattern = pattern[2:]
			case strings.HasPrefix(pattern, "~/"):
				root = s.options.Home
				anchored = true
				pattern = pattern[2:]
			case strings.HasPrefix(pattern, "/"):
				anchored = true
				root = s.options.PermissionRoots[source]
				if root == "" {
					return false, false, capability("settings-source root for Edit permission rule " + source)
				}
				pattern = pattern[1:]
			default:
				root = s.options.CWD
				pattern = strings.TrimPrefix(pattern, "./")
			}
			if !filepath.IsAbs(root) {
				return false, false, capability("absolute settings-source permission root")
			}
			target := filepath.Join(root, pattern)
			canonicalTarget := target
			for ancestor := target; ; ancestor = filepath.Dir(ancestor) {
				if resolved, e := s.options.Files.Realpath(ancestor); e == nil {
					suffix, _ := filepath.Rel(ancestor, target)
					canonicalTarget = filepath.Join(resolved, suffix)
					break
				}
				if filepath.Dir(ancestor) == ancestor {
					break
				}
			}
			// Literal slash-bearing patterns match path/descendants; basename patterns
			// use ignore's any-depth semantics within their root.
			relative, e := filepath.Rel(root, path)
			if e != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, "../") {
				continue
			}
			if anchored || strings.Contains(pattern, "/") {
				if path == target || strings.HasPrefix(path, target+string(filepath.Separator)) || path == canonicalTarget || strings.HasPrefix(path, canonicalTarget+string(filepath.Separator)) {
					editDeny = true
				}
			} else {
				for _, part := range strings.Split(relative, string(filepath.Separator)) {
					if part == pattern {
						editDeny = true
					}
				}
			}
		}
	}
	return
}
func (s *Service) denyForPath(scope *cooperative.Scope, path string) (protocol.JSONObject, bool, bool, error) {
	context, e := s.options.PermissionContext(scope)
	if e != nil {
		return protocol.JSONObject{}, false, false, e
	}
	if !context.IsObject() {
		return context, false, false, capability("live ToolPermissionContext")
	}
	whole, edit, e := s.rules(scope, context, path, false)
	if e != nil || whole {
		return context, whole, edit, e
	}
	// Source checks original and fully resolved paths, including symlink parents
	// of a new file. Find the first resolvable ancestor without extra content IO.
	parent := path
	for {
		resolved, e := s.options.Files.Realpath(parent)
		if e == nil {
			suffix, e := filepath.Rel(parent, path)
			if e != nil {
				return context, false, false, e
			}
			resolved = filepath.Join(resolved, suffix)
			_, aliasDeny, e := s.rules(scope, context, resolved, false)
			if e != nil {
				return context, false, false, e
			}
			edit = edit || aliasDeny
			break
		}
		if !errors.Is(e, fs.ErrNotExist) {
			break
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	return context, whole, edit, nil
}
func (s *Service) Permission(scope *cooperative.Scope, input protocol.JSONObject) (string, error) {
	if e := s.check(scope); e != nil {
		return "", e
	}
	if s.options.Host.Platform == "windows" {
		path, err := s.expand(input.Get("file_path"))
		if err != nil {
			return "", err
		}
		context, err := s.options.PermissionContext(scope)
		if err != nil {
			return "", err
		}
		whole, deny, err := s.windowsDenyRules(context, path, false)
		if err != nil {
			return "", err
		}
		if whole || deny {
			return "deny", nil
		}
		if context.String("mode") == "bypassPermissions" || context.String("mode") == "plan" && string(context.Get("isBypassPermissionsModeAvailable")) == "true" {
			return "allow", nil
		}
		// The real tool catalog supplies the full shared resolver including
		// acceptEdits safety. This standalone writer never invents a grant.
		return "ask", nil
	}
	if s.options.Host.Platform != "windows" && unc(input.Get("file_path")) {
		return "", capability("UNC/NTLM network-path permission handling")
	}
	path, e := s.expand(input.Get("file_path"))
	if e != nil {
		return "", e
	}
	context, whole, edit, e := s.denyForPath(scope, path)
	if e != nil {
		return "", e
	}
	if whole || edit {
		return "deny", nil
	}
	mode, e := scalar(context.Get("mode"))
	if e != nil {
		return "", e
	}
	bypass := mode == "bypassPermissions"
	if mode == "plan" {
		var enabled bool
		e = json.Unmarshal(context.Get("isBypassPermissionsModeAvailable"), &enabled)
		if e != nil {
			return "", e
		}
		bypass = enabled
	}
	if bypass {
		return "allow", nil
	}
	return "", capability("normal/acceptEdits/auto permission safety, internal paths and ask resolver")
}
