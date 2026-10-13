package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"path/filepath"
)

func (s *Service) matchWindowsPathRule(c protocol.JSONObject, key, name, path string) (bool, error) {
	tool := "Edit"
	if name == "Read" || name == "Glob" || name == "Grep" {
		tool = "Read"
	}
	patterns, err := nativepaths.WindowsPatterns(c, key, tool)
	if err != nil {
		return false, err
	}
	return nativepaths.MatchWindowsPatterns(path, s.options.CWD, s.options.Home, s.options.PermissionRoots, patterns)
}

func (s *Service) workingPathContains(path, root string) bool {
	if s.options.Platform != "windows" {
		return pathInWorkingPath(path, root)
	}
	path, err := nativepaths.ExpandWindows(path, s.options.CWD, s.options.Home)
	if err != nil {
		return false
	}
	root, err = nativepaths.ExpandWindows(root, s.options.CWD, s.options.Home)
	if err != nil {
		return false
	}
	lower := cases.Lower(language.Und)
	relative := nativepaths.RelativeWindows(lower.String(root), lower.String(path))
	return relative == "" || relative != ".." && !regexpTraversal.MatchString(relative)
}

// UNC path inspection must stay lexical until an actual permission decision.
// Local Windows paths still retain native canonical/symlink escape checks.
func (s *Service) permissionResolved(path string) string {
	if s.options.Platform == "windows" && nativepaths.IsUNC(path) {
		return path
	}
	if s.options.PermissionRealpath != nil {
		for ancestor := path; ; {
			if resolved, err := s.options.PermissionRealpath(ancestor); err == nil {
				if s.options.Platform == "windows" {
					return nativepaths.JoinWindows(resolved, nativepaths.RelativeWindows(ancestor, path))
				}
				relative, _ := filepath.Rel(ancestor, path)
				return filepath.Join(resolved, relative)
			}
			next := filepath.Dir(ancestor)
			if s.options.Platform == "windows" {
				next = nativepaths.DirWindows(ancestor)
			}
			if next == ancestor {
				return path
			}
			ancestor = next
		}
	}
	return resolved(path)
}
