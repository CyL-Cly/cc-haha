package worktree

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

var traversal = regexp.MustCompile(`(?:^|[\\/])\.\.(?:[\\/]|$)`)

func (s *Service) PostSetup(ctx context.Context, root, path string, settings Settings) error {
	relative := settings.LocalSettingsPath
	if relative == "" {
		relative = ".claude/settings.local.json"
	}
	destination := filepath.Join(path, relative)
	e := s.options.Files.MkdirAll(filepath.Dir(destination), 0777)
	if e == nil {
		e = s.options.Files.CopyFile(filepath.Join(root, relative), destination)
	}
	if e != nil && !errors.Is(e, fs.ErrNotExist) {
		s.options.Log("warn", "Failed to copy settings.local.json: "+e.Error())
	}
	hookPath := ""
	husky := filepath.Join(root, ".husky")
	for _, candidate := range []string{husky, filepath.Join(root, ".git/hooks")} {
		if info, e := s.options.Files.Stat(candidate); e == nil && info.IsDir() {
			hookPath = candidate
			break
		}
	}
	if hookPath != "" {
		dir := s.GitDir(root)
		existing := (*string)(nil)
		if dir != "" {
			existing = ParseConfigValue(s.read(filepath.Join(s.CommonDir(dir), "config")), "core", nil, "hooksPath")
		}
		if existing == nil || *existing != hookPath {
			result, e := s.run(ctx, path, []string{"config", "core.hooksPath", hookPath}, false)
			if e != nil {
				return e
			}
			if result.Code != 0 {
				s.options.Log("error", "Failed to configure hooks path: "+result.Stderr)
			}
		}
	}
	for _, dir := range settings.SymlinkDirectories {
		if traversal.MatchString(dir) {
			s.options.Log("warn", "Skipping symlink for \""+dir+"\": path traversal detected")
			continue
		}
		e := s.options.Files.Symlink(filepath.Join(root, dir), filepath.Join(path, dir))
		if e != nil && !errors.Is(e, fs.ErrNotExist) && !errors.Is(e, fs.ErrExist) {
			s.options.Log("warn", "Failed to symlink "+dir+": "+e.Error())
		}
	}
	if _, e := s.CopyInclude(ctx, root, path); e != nil {
		return e
	}
	if s.options.CommitAttributionHook != nil {
		worktreeHooks := ""
		if hookPath == husky {
			worktreeHooks = filepath.Join(path, ".husky")
		}
		if e := s.options.CommitAttributionHook(ctx, path, worktreeHooks); e != nil {
			s.options.Log("warn", "Failed to install attribution hook in worktree: "+e.Error())
		}
	}
	return nil
}
func (s *Service) CopyInclude(ctx context.Context, root, path string) ([]string, error) {
	b, e := s.options.Files.ReadFile(filepath.Join(root, ".worktreeinclude"))
	if e != nil {
		return []string{}, nil
	}
	content := string(b)
	patterns := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = trim(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	if len(patterns) == 0 {
		return []string{}, nil
	}
	if s.options.IncludeMatcher == nil {
		return nil, &CapabilityError{"actual .worktreeinclude ignore matcher"}
	}
	result, e := s.run(ctx, root, []string{"ls-files", "--others", "--ignored", "--exclude-standard", "--directory"}, false)
	if e != nil {
		return nil, e
	}
	if result.Code != 0 || trim(result.Stdout) == "" {
		return []string{}, nil
	}
	files, expand := []string{}, []string{}
	for _, entry := range strings.Split(trim(result.Stdout), "\n") {
		if entry == "" {
			continue
		}
		if !strings.HasSuffix(entry, "/") {
			matched, e := s.options.IncludeMatcher(content, entry)
			if e != nil {
				return nil, e
			}
			if matched {
				files = append(files, entry)
			}
			continue
		}
		shouldExpand := false
		for _, pattern := range patterns {
			normalized := strings.TrimPrefix(pattern, "/")
			if strings.HasPrefix(normalized, entry) {
				shouldExpand = true
				break
			}
			index := strings.IndexAny(normalized, "*?[")
			if index > 0 && strings.HasPrefix(entry, normalized[:index]) {
				shouldExpand = true
				break
			}
		}
		if !shouldExpand {
			shouldExpand, e = s.options.IncludeMatcher(content, strings.TrimSuffix(entry, "/"))
			if e != nil {
				return nil, e
			}
		}
		if shouldExpand {
			expand = append(expand, entry)
		}
	}
	if len(expand) > 0 {
		result, e = s.run(ctx, root, append([]string{"ls-files", "--others", "--ignored", "--exclude-standard", "--"}, expand...), false)
		if e != nil {
			return nil, e
		}
		if result.Code == 0 {
			for _, file := range strings.Split(trim(result.Stdout), "\n") {
				if file != "" {
					matched, e := s.options.IncludeMatcher(content, file)
					if e != nil {
						return nil, e
					}
					if matched {
						files = append(files, file)
					}
				}
			}
		}
	}
	copied := []string{}
	for _, file := range files {
		destination := filepath.Join(path, file)
		e = s.options.Files.MkdirAll(filepath.Dir(destination), 0777)
		if e == nil {
			e = s.options.Files.CopyFile(filepath.Join(root, file), destination)
		}
		if e != nil {
			s.options.Log("warn", "Failed to copy "+file+" to worktree: "+e.Error())
		} else {
			copied = append(copied, file)
		}
	}
	return copied, nil
}
