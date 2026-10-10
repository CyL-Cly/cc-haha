package skillcommands

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type markdownFile struct {
	path, base, source string
	p                  markdownfrontmatter.Parsed
}

func (s *Service) markdownPaths(root string, plugin bool) []string {
	files := []string{}
	visited := []fs.FileInfo{}
	var walk func(string)
	walk = func(dir string) {
		if !plugin {
			info, e := s.options.Files.Stat(dir)
			if e != nil {
				return
			}
			for _, v := range visited {
				if os.SameFile(info, v) {
					return
				}
			}
			visited = append(visited, info)
		}
		entries, e := s.options.Files.ReadDir(dir)
		if e != nil {
			s.fail(e)
			return
		}
		leaf := false
		if plugin {
			for _, entry := range entries {
				if entry.Type().IsRegular() && strings.EqualFold(entry.Name(), "SKILL.md") {
					leaf = true
				}
			}
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				if !leaf {
					walk(path)
				}
			} else if entry.Type().IsRegular() {
				name := entry.Name()
				if plugin {
					name = strings.ToLower(name)
				}
				if strings.HasSuffix(name, ".md") {
					files = append(files, path)
				}
			} else if !plugin && entry.Type()&fs.ModeSymlink != 0 {
				info, e := s.options.Files.Stat(path)
				if e == nil {
					if info.IsDir() {
						walk(path)
					} else if info.Mode().IsRegular() && strings.HasSuffix(entry.Name(), ".md") {
						files = append(files, path)
					}
				}
			}
		}
	}
	walk(root)
	if !plugin {
		sort.Strings(files)
	}
	return files
}
func (s *Service) readFile(path, base, source string) (markdownFile, error) {
	raw, e := s.options.Files.ReadFile(path)
	if e != nil {
		return markdownFile{}, e
	}
	p, e := markdownfrontmatter.Parse(string(protocol.DecodeJavaScriptUTF8(raw)))
	return markdownFile{path, base, source, p}, e
}
func transform(files []markdownFile) []markdownFile {
	order := []string{}
	dirs := map[string][]markdownFile{}
	for _, file := range files {
		dir := filepath.Dir(file.path)
		if _, ok := dirs[dir]; !ok {
			order = append(order, dir)
		}
		dirs[dir] = append(dirs[dir], file)
	}
	out := []markdownFile{}
	for _, dir := range order {
		group := dirs[dir]
		found := false
		for _, file := range group {
			if strings.EqualFold(filepath.Base(file.path), "SKILL.md") {
				out = append(out, file)
				found = true
				break
			}
		}
		if !found {
			out = append(out, group...)
		}
	}
	return out
}
func (s *Service) legacy(ctx context.Context, cwd string) ([]entry, error) {
	roots := []struct{ path, source string }{{filepath.Join(s.options.ManagedDir, ".claude", "commands"), "policySettings"}}
	user, e := s.options.EnabledSource(ctx, "userSettings")
	if e != nil {
		return nil, e
	}
	project, e := s.options.EnabledSource(ctx, "projectSettings")
	if e != nil {
		return nil, e
	}
	if user {
		roots = append(roots, struct{ path, source string }{filepath.Join(s.options.ConfigDir, "commands"), "userSettings"})
	}
	projectDirs := []string{}
	for _, dir := range s.options.Roots.WalkProjectDirs(cwd) {
		path := filepath.Join(dir, ".claude", "commands")
		_, e := s.options.Files.Stat(path)
		if e == nil {
			projectDirs = append(projectDirs, path)
		} else if !inaccessible(e) {
			return nil, e
		}
	}
	root := s.gitRoot(cwd)
	canonical := s.canonical(root)
	if root != "" && canonical != root {
		worktree := filepath.Join(root, ".claude", "commands")
		present := false
		for _, dir := range projectDirs {
			if dir == worktree {
				present = true
			}
		}
		if !present {
			main := filepath.Join(canonical, ".claude", "commands")
			duplicate := false
			for _, dir := range projectDirs {
				if dir == main {
					duplicate = true
				}
			}
			if !duplicate {
				projectDirs = append(projectDirs, main)
			}
		}
	}
	if project {
		for _, path := range projectDirs {
			roots = append(roots, struct{ path, source string }{path, "projectSettings"})
		}
	}
	files := []markdownFile{}
	identities := []fs.FileInfo{}
	lstat := s.options.Files.Stat
	if f, ok := s.options.Files.(interface {
		Lstat(string) (fs.FileInfo, error)
	}); ok {
		lstat = f.Lstat
	}
	for _, root := range roots {
		for _, path := range s.markdownPaths(root.path, false) {
			f, e := s.readFile(path, root.path, root.source)
			if e != nil {
				s.fail(e)
				continue
			}
			info, e := lstat(path)
			duplicate := false
			if e == nil {
				for _, known := range identities {
					if os.SameFile(known, info) {
						duplicate = true
						break
					}
				}
			}
			if duplicate {
				continue
			}
			if e == nil {
				identities = append(identities, info)
			}
			files = append(files, f)
		}
	}
	out := []entry{}
	for _, file := range transform(files) {
		skill := strings.EqualFold(filepath.Base(file.path), "SKILL.md")
		target := filepath.Dir(file.path)
		name := strings.TrimSuffix(filepath.Base(file.path), ".md")
		base := ""
		if skill {
			base = target
			name = filepath.Base(target)
			target = filepath.Dir(target)
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(target, file.base), string(filepath.Separator))
		if rel != "" {
			name = strings.ReplaceAll(rel, string(filepath.Separator), ":") + ":" + name
		}
		d, e := create(ctx, file.p, name, file.source, base, "commands_DEPRECATED", "Custom command", s.options.ResolveModel)
		if e != nil {
			s.fail(e)
			continue
		}
		set(d.metadata, "userFacingName", name)
		out = append(out, entry{definition: d, path: file.path})
	}
	return out, nil
}
func inaccessible(e error) bool {
	return errors.Is(e, fs.ErrNotExist) || errors.Is(e, fs.ErrPermission) || errors.Is(e, syscall.ENOTDIR) || errors.Is(e, syscall.ELOOP)
}
func (s *Service) gitRoot(start string) string {
	if start == "" {
		return ""
	}
	for d := filepath.Clean(start); ; d = filepath.Dir(d) {
		if _, e := s.options.Files.Stat(filepath.Join(d, ".git")); e == nil {
			return d
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}
func (s *Service) canonical(root string) string {
	raw, e := s.options.Files.ReadFile(filepath.Join(root, ".git"))
	if e != nil {
		return root
	}
	text := trimJS(string(raw))
	if !strings.HasPrefix(text, "gitdir:") {
		return root
	}
	resolve := func(base, path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(base, path)
	}
	dir := resolve(root, trimJS(strings.TrimPrefix(text, "gitdir:")))
	raw, e = s.options.Files.ReadFile(filepath.Join(dir, "commondir"))
	if e != nil {
		return root
	}
	common := resolve(dir, trimJS(string(raw)))
	if filepath.Dir(dir) != filepath.Join(common, "worktrees") {
		return root
	}
	raw, e = s.options.Files.ReadFile(filepath.Join(dir, "gitdir"))
	if e != nil {
		return root
	}
	back, e := s.options.Files.Realpath(trimJS(string(raw)))
	if e != nil {
		return root
	}
	actual, e := s.options.Files.Realpath(root)
	if e != nil || back != filepath.Join(actual, ".git") {
		return root
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}
