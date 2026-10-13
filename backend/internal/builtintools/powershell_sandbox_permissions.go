package builtintools

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
)

// Keep intermediate file-link destinations as well as the lexical and final
// canonical paths. An allow at the final destination cannot erase a deny on
// an intermediate link, and dangling/new targets use the deepest real parent.
func (s *Service) powerShellSandboxPathForms(value string) []string {
	if value == "~" || strings.HasPrefix(value, "~/") {
		value = s.options.Home + value[1:]
	}
	if s.options.Platform == "windows" {
		value = nativepaths.ResolveWindows(value, s.options.CWD)
	} else if !filepath.IsAbs(value) {
		value = filepath.Join(s.options.CWD, value)
	}
	paths := []string{value}
	seen := map[string]bool{value: true}
	add := func(value string) {
		if !seen[value] {
			seen[value] = true
			paths = append(paths, value)
		}
	}
	if nativepaths.IsUNC(value) {
		return paths
	}
	current := value
	visited := map[string]bool{}
	for depth := 0; depth < 40 && !visited[current]; depth++ {
		visited[current] = true
		if _, err := os.Stat(current); err != nil {
			if current == value {
				add(s.powerShellDeepestExistingPath(value))
			}
			break
		}
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice|os.ModeCharDevice) != 0 || info.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(current)
		if err != nil {
			break
		}
		if s.options.Platform == "windows" {
			target = nativepaths.ResolveWindows(target, nativepaths.DirWindows(current))
		} else if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		add(target)
		current = target
	}
	// safeResolvePath refuses special files, just as the original permissions
	// helper does. The final canonical form includes directory component links.
	if info, err := os.Lstat(value); err == nil && info.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice|os.ModeCharDevice) == 0 {
		if target, err := filepath.EvalSymlinks(value); err == nil {
			add(target)
		}
	}
	return paths
}

func (s *Service) powerShellDeepestExistingPath(value string) string {
	directory := filepath.Dir
	if s.options.Platform == "windows" {
		directory = nativepaths.DirWindows
	}
	for ancestor := value; ancestor != directory(ancestor); ancestor = directory(ancestor) {
		info, err := os.Lstat(ancestor)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err != nil && info.Mode()&os.ModeSymlink != 0 {
			resolved, err = os.Readlink(ancestor)
			if err == nil {
				if s.options.Platform == "windows" {
					resolved = nativepaths.ResolveWindows(resolved, directory(ancestor))
				} else if !filepath.IsAbs(resolved) {
					resolved = filepath.Join(directory(ancestor), resolved)
				}
			}
		}
		if err != nil {
			return value
		}
		if s.options.Platform == "windows" {
			return nativepaths.JoinWindows(resolved, nativepaths.RelativeWindows(ancestor, value))
		}
		suffix, _ := filepath.Rel(ancestor, value)
		return filepath.Join(resolved, suffix)
	}
	return value
}

func (s *Service) powerShellSandboxWriteAllowed(scope *cooperative.Scope, path string) (bool, error) {
	binding := s.options.BashSandbox
	if binding == nil || binding.WritePaths == nil {
		return false, nil
	}
	if !scope.BelongsTo(binding.Owner) || binding.Owner != s.options.Owner {
		return false, cooperative.ErrRestricted
	}
	config, err := binding.WritePaths(scope)
	if err != nil || config == nil {
		return false, err
	}
	allow, deny := []string{}, []string{}
	for _, root := range config.AllowOnly {
		allow = append(allow, s.powerShellSandboxPathForms(root)...)
	}
	for _, root := range config.DenyWithinAllow {
		deny = append(deny, s.powerShellSandboxPathForms(root)...)
	}
	for _, target := range s.powerShellSandboxPathForms(path) {
		for _, root := range deny {
			if s.workingPathContains(target, root) {
				return false, nil
			}
		}
		allowed := false
		for _, root := range allow {
			if s.workingPathContains(target, root) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false, nil
		}
	}
	return true, nil
}
