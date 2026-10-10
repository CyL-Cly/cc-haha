package agentsapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

func within(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != "" && relative != "." && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func canonicalPotential(path string) (string, error) {
	ancestor := filepath.Clean(path)
	missing := []string{}
	for {
		real, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			return filepath.Join(append([]string{real}, missing...)...), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", err
		}
		missing = append([]string{filepath.Base(ancestor)}, missing...)
		ancestor = parent
	}
}
func (s *Service) disjoint(project string) error {
	a, err := canonicalPotential(project)
	if err != nil {
		return err
	}
	b, err := canonicalPotential(filepath.Join(s.options.ConfigDir, "agents"))
	if err != nil {
		return err
	}
	if a == b || within(a, b) || within(b, a) {
		return fail(403, "AGENT_SCOPE_COLLISION", "Project and user agent roots must not overlap")
	}
	return nil
}
func unsafe(message string) error { return fail(403, "UNSAFE_AGENT_DIRECTORY", message) }
func directoryChain(anchor string, parts []string, create bool) (string, error) {
	current := anchor
	for index, part := range parts {
		candidate := filepath.Join(current, part)
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			if !create {
				return filepath.Join(append([]string{current}, parts[index:]...)...), nil
			}
			if err = os.Mkdir(candidate, 0755); err != nil && !os.IsExist(err) {
				return "", err
			}
			info, err = os.Lstat(candidate)
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", unsafe("Agent directory component must be a real directory: " + part)
		}
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", err
		}
		if real != anchor && !within(anchor, real) {
			return "", unsafe("Agent directory escapes its trusted root")
		}
		current = real
	}
	return current, nil
}
func (s *Service) userDirectory(create bool) (string, error) {
	configured := filepath.Clean(s.options.ConfigDir)
	if create {
		if err := os.MkdirAll(configured, 0755); err != nil {
			return "", err
		}
	}
	info, err := os.Lstat(configured)
	if os.IsNotExist(err) && !create {
		return filepath.Join(configured, "agents"), nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		info, err = os.Stat(configured)
		if err != nil {
			return "", err
		}
	}
	if !info.IsDir() {
		return "", unsafe("Claude config root must be a directory")
	}
	real, err := filepath.EvalSymlinks(configured)
	if err != nil {
		return "", err
	}
	return directoryChain(real, []string{"agents"}, create)
}
func gitRoot(cwd string) string {
	for current := filepath.Clean(cwd); ; current = filepath.Dir(current) {
		info, err := os.Stat(filepath.Join(current, ".git"))
		if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return norm.NFC.String(current)
		}
		if current == filepath.Dir(current) {
			return ""
		}
	}
}
func canonicalGitRoot(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root
	}
	text := trim(string(raw))
	if !strings.HasPrefix(text, "gitdir:") {
		return root
	}
	resolve := func(base, value string) string {
		if filepath.IsAbs(value) {
			return filepath.Clean(value)
		}
		return filepath.Join(base, value)
	}
	gitdir := resolve(root, trim(text[len("gitdir:"):]))
	raw, err = os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return root
	}
	common := resolve(gitdir, trim(string(raw)))
	if filepath.Dir(gitdir) != filepath.Join(common, "worktrees") {
		return root
	}
	raw, err = os.ReadFile(filepath.Join(gitdir, "gitdir"))
	if err != nil {
		return root
	}
	back, err := filepath.EvalSymlinks(trim(string(raw)))
	if err != nil {
		return root
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil || back != filepath.Join(real, ".git") {
		return root
	}
	if filepath.Base(common) == ".git" {
		return norm.NFC.String(filepath.Dir(common))
	}
	return norm.NFC.String(common)
}
func (s *Service) projectAnchor(context MutationContext) (string, string, error) {
	cwd, err := filepath.EvalSymlinks(s.cwd(trim(context.CWD)))
	if err != nil {
		return "", "", bad("Agent project cwd must be an existing directory")
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return "", "", bad("Agent project cwd must be an existing directory")
	}
	root := gitRoot(cwd)
	if root == "" {
		root = cwd
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	return cwd, real, nil
}
func realDirectory(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink == 0 && info.IsDir(), nil
}
func (s *Service) projectDirectory(context MutationContext, create bool) (string, error) {
	_, root, err := s.projectAnchor(context)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, ".claude", "agents")
	if err = s.disjoint(candidate); err != nil {
		return "", err
	}
	if create {
		canonical := canonicalGitRoot(root)
		if canonical != root {
			real, err := filepath.EvalSymlinks(canonical)
			if err != nil {
				return "", err
			}
			if err = s.disjoint(filepath.Join(real, ".claude", "agents")); err != nil {
				return "", err
			}
			work, err := directoryChain(root, []string{".claude", "agents"}, false)
			if err != nil {
				return "", err
			}
			exists, err := realDirectory(work)
			if err != nil {
				return "", err
			}
			if !exists {
				main, err := directoryChain(real, []string{".claude", "agents"}, false)
				if err != nil {
					return "", err
				}
				exists, err = realDirectory(main)
				if err != nil {
					return "", err
				}
				if exists {
					if err = s.disjoint(main); err != nil {
						return "", err
					}
					return main, nil
				}
			}
		}
	}
	out, err := directoryChain(root, []string{".claude", "agents"}, create)
	if err != nil {
		return "", err
	}
	if err = s.disjoint(out); err != nil {
		return "", err
	}
	return out, nil
}
func (s *Service) directory(context MutationContext, create bool) (string, error) {
	if context.Scope == "user" {
		return s.userDirectory(create)
	}
	if context.Scope == "project" {
		return s.projectDirectory(context, create)
	}
	return "", bad("Agent scope must be \"user\" or \"project\"")
}
func (s *Service) loadedProjectDirectory(base string) (string, error) {
	base = s.cwd(base)
	if filepath.Base(base) != "agents" || filepath.Base(filepath.Dir(base)) != ".claude" {
		return "", nil
	}
	anchor := filepath.Dir(filepath.Dir(base))
	real, err := filepath.EvalSymlinks(anchor)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", nil
	}
	safe, err := directoryChain(real, []string{".claude", "agents"}, false)
	if err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Code == "UNSAFE_AGENT_DIRECTORY" || os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if err = s.disjoint(safe); err != nil {
		return "", err
	}
	return safe, nil
}
func (s *Service) resolveTarget(ctx context.Context, name string, mutation MutationContext) (string, string, error) {
	if mutation.Target == "" {
		directory, err := s.directory(mutation, false)
		return directory, filepath.Join(directory, name+".md"), err
	}
	if !filepath.IsAbs(mutation.Target) || !strings.HasSuffix(mutation.Target, ".md") {
		return "", "", bad("Agent target must be an absolute Markdown file path")
	}
	requested, err := fileStat(mutation.Target)
	if err != nil {
		return "", "", err
	}
	if requested == nil {
		return "", "", fail(404, "NOT_FOUND", "Agent target not found: "+name)
	}
	target, err := filepath.EvalSymlinks(mutation.Target)
	if err != nil {
		return "", "", fail(404, "NOT_FOUND", "Agent target not found: "+name)
	}
	real, err := fileStat(target)
	if err != nil {
		return "", "", err
	}
	if real == nil {
		return "", "", fail(404, "NOT_FOUND", "Agent target not found: "+name)
	}
	if err = matchingIdentity(real, requested); err != nil {
		return "", "", err
	}
	roots := []string{}
	if mutation.Scope == "user" {
		directory, err := s.userDirectory(false)
		if err != nil {
			return "", "", err
		}
		roots = append(roots, directory)
	} else if mutation.Scope == "project" {
		_, root, err := s.projectAnchor(mutation)
		if err != nil {
			return "", "", err
		}
		if err = s.disjoint(filepath.Join(root, ".claude", "agents")); err != nil {
			return "", "", err
		}
		canonical := canonicalGitRoot(root)
		if canonical != root {
			real, err := filepath.EvalSymlinks(canonical)
			if err != nil {
				return "", "", err
			}
			if err = s.disjoint(filepath.Join(real, ".claude", "agents")); err != nil {
				return "", "", err
			}
		}
		all, err := s.options.Load(ctx, s.cwd(trim(mutation.CWD)))
		if err != nil {
			return "", "", err
		}
		seen := map[string]bool{}
		for _, definition := range all.All {
			if definition.Source != "projectSettings" || definition.FilePath == "" {
				continue
			}
			directory := definition.Fields.String("baseDir")
			safe, err := s.loadedProjectDirectory(directory)
			if err != nil {
				return "", "", err
			}
			if safe != "" && !seen[safe] {
				roots = append(roots, safe)
				seen[safe] = true
			}
		}
	} else {
		return "", "", bad("Agent scope must be \"user\" or \"project\"")
	}
	loaded, err := s.options.Load(ctx, s.cwd(trim(mutation.CWD)))
	if err != nil {
		return "", "", err
	}
	source := "userSettings"
	if mutation.Scope == "project" {
		source = "projectSettings"
	}
	found := false
	for _, definition := range loaded.All {
		if definition.Source != source || definition.FilePath == "" {
			continue
		}
		candidate, err := filepath.EvalSymlinks(definition.FilePath)
		if err != nil || candidate != target {
			continue
		}
		if definition.Name != name {
			return "", "", fail(409, "CONFLICT", "Agent target identity does not match requested agent: "+name)
		}
		found = true
		break
	}
	if !found {
		return "", "", fail(403, "AGENT_SCOPE_MISMATCH", "Agent target does not belong to the requested scope")
	}
	for _, root := range roots {
		real, err := filepath.EvalSymlinks(root)
		if err == nil && within(real, target) {
			return root, target, nil
		}
	}
	return "", "", fail(403, "AGENT_SCOPE_MISMATCH", "Agent target does not belong to the requested scope")
}
