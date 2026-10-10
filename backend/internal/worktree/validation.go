package worktree

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

var segmentPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func ValidateSlug(slug string) error {
	if length := len(utf16.Encode([]rune(slug))); length > 64 {
		return fmt.Errorf("Invalid worktree name: must be 64 characters or fewer (got %d)", length)
	}
	for _, segment := range strings.Split(slug, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("Invalid worktree name \"%s\": must not contain \".\" or \"..\" path segments", slug)
		}
		if !segmentPattern.MatchString(segment) {
			return fmt.Errorf("Invalid worktree name \"%s\": each \"/\"-separated segment must be non-empty and contain only letters, digits, dots, underscores, and dashes", slug)
		}
	}
	return nil
}
func BranchName(slug string) string { return "worktree-" + strings.ReplaceAll(slug, "/", "+") }
func PathFor(root, slug string) string {
	return filepath.Join(root, ".claude/worktrees", strings.ReplaceAll(slug, "/", "+"))
}
func contained(root, path string) bool {
	r, e := filepath.Rel(root, path)
	return e == nil && !filepath.IsAbs(r) && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
func (s *Service) SafeLocation(root, path string) error {
	root, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	managed := filepath.Join(root, ".claude/worktrees")
	display := managed
	if path != "" {
		display, e = filepath.Abs(path)
		if e != nil {
			return e
		}
	}
	refuse := func(reason string) error {
		return fmt.Errorf("Refusing to use unsafe worktree path %q: %s", display, reason)
	}
	if !contained(root, managed) {
		return refuse("the worktrees directory is outside the repository")
	}
	if path != "" && filepath.Dir(display) != managed {
		return refuse("the worktree is not a direct child of .claude/worktrees")
	}
	paths := []string{filepath.Join(root, ".claude"), managed}
	if path != "" {
		paths = append(paths, display)
	}
	for _, p := range paths {
		st, e := s.options.Files.Lstat(p)
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			return refuse("cannot inspect " + p + ": " + e.Error())
		}
		if st.Mode()&fs.ModeSymlink != 0 {
			return refuse(p + " is a symbolic link or junction")
		}
	}
	canonical, e := s.options.Files.Realpath(root)
	if e != nil {
		return refuse("cannot resolve the repository root: " + e.Error())
	}
	for ancestor := display; ; ancestor = filepath.Dir(ancestor) {
		resolved, e := s.options.Files.Realpath(ancestor)
		if e != nil {
			if !errors.Is(e, fs.ErrNotExist) {
				return refuse("cannot resolve " + ancestor + ": " + e.Error())
			}
			if filepath.Dir(ancestor) == ancestor {
				return refuse("no existing ancestor can be verified")
			}
			continue
		}
		if !contained(canonical, resolved) {
			return refuse("its canonical path escapes the repository")
		}
		return nil
	}
}
