package agentcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func inaccessible(e error) bool {
	return errors.Is(e, fs.ErrNotExist) || errors.Is(e, fs.ErrPermission) || errors.Is(e, syscall.ENOTDIR) || errors.Is(e, syscall.ELOOP)
}
func (s *Service) gitRoot(start string) string {
	for d := filepath.Clean(start); ; d = filepath.Dir(d) {
		i, e := s.options.Files.Stat(filepath.Join(d, ".git"))
		if e == nil && (i.IsDir() || i.Mode().IsRegular()) {
			return norm.NFC.String(d)
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	return ""
}
func (s *Service) canonical(root string) string {
	f := s.options.Files
	b, e := f.ReadFile(filepath.Join(root, ".git"))
	if e != nil {
		return root
	}
	text := strings.TrimSpace(string(b))
	if !strings.HasPrefix(text, "gitdir:") {
		return root
	}
	resolve := func(base, value string) string {
		if filepath.IsAbs(value) {
			return filepath.Clean(value)
		}
		return filepath.Join(base, value)
	}
	gitdir := resolve(root, strings.TrimSpace(text[len("gitdir:"):]))
	b, e = f.ReadFile(filepath.Join(gitdir, "commondir"))
	if e != nil {
		return root
	}
	common := resolve(gitdir, strings.TrimSpace(string(b)))
	if filepath.Dir(gitdir) != filepath.Join(common, "worktrees") {
		return root
	}
	b, e = f.ReadFile(filepath.Join(gitdir, "gitdir"))
	if e != nil {
		return root
	}
	back, e := f.Realpath(strings.TrimSpace(string(b)))
	if e != nil {
		return root
	}
	real, e := f.Realpath(root)
	if e != nil || back != filepath.Join(real, ".git") {
		return root
	}
	if filepath.Base(common) == ".git" {
		return norm.NFC.String(filepath.Dir(common))
	}
	return norm.NFC.String(common)
}
func (s *Service) projectDirs() ([]string, error) {
	root := s.gitRoot(s.options.CWD)
	session := s.gitRoot(s.options.ProjectRoot)
	if root != "" && session != "" {
		canonical := s.canonical(root)
		if canonical != session && root != session && strings.HasPrefix(root, session+string(filepath.Separator)) {
			root = session
		}
	}
	dirs := []string{}
	for d := filepath.Clean(s.options.CWD); norm.NFC.String(d) != norm.NFC.String(filepath.Clean(s.options.Home)); d = filepath.Dir(d) {
		p := filepath.Join(d, ".claude/agents")
		_, e := s.options.Files.Stat(p)
		if e == nil {
			dirs = append(dirs, p)
		} else if !inaccessible(e) {
			return nil, e
		}
		if d == root || d == filepath.Dir(d) {
			break
		}
	}
	git := s.gitRoot(s.options.CWD)
	if git != "" {
		canonical := s.canonical(git)
		if canonical != git {
			present := false
			for _, p := range dirs {
				if p == filepath.Join(git, ".claude/agents") {
					present = true
				}
			}
			if !present {
				p := filepath.Join(canonical, ".claude/agents")
				exists := false
				for _, v := range dirs {
					if v == p {
						exists = true
					}
				}
				if !exists {
					dirs = append(dirs, p)
				}
			}
		}
	}
	return dirs, nil
}
func (s *Service) find(dir string) ([]string, error) {
	if !boolEnv(s.options.Environment("CLAUDE_CODE_USE_NATIVE_FILE_SEARCH")) {
		if s.options.FindMarkdown != nil {
			return s.options.FindMarkdown(dir)
		}
		return nil, cap("default ripgrep file-search process")
	}
	deadline := s.options.Now().Add(3e9)
	visited := map[string]bool{}
	files := []string{}
	var walk func(string) error
	walk = func(p string) error {
		if !s.options.Now().Before(deadline) {
			return nil
		}
		info, e := s.options.Files.Stat(p)
		if e != nil {
			var c *CapabilityError
			if errors.As(e, &c) {
				return e
			}
			return nil
		}
		if info.IsDir() {
			key := s.options.Files.Identity(info)
			if key == "" {
				key, e = s.options.Files.Realpath(p)
				if e != nil {
					return nil
				}
			}
			if visited[key] {
				return nil
			}
			visited[key] = true
		}
		entries, e := s.options.Files.ReadDir(p)
		if e != nil {
			var c *CapabilityError
			if errors.As(e, &c) {
				return e
			}
			return nil
		}
		for _, entry := range entries {
			if !s.options.Now().Before(deadline) {
				break
			}
			path := filepath.Join(p, entry.Name())
			i := entry.Type()
			if i&fs.ModeSymlink != 0 {
				info, e = s.options.Files.Stat(path)
				if e != nil {
					var c *CapabilityError
					if errors.As(e, &c) {
						return e
					}
					continue
				}
				i = info.Mode()
			}
			if i.IsDir() {
				if e = walk(path); e != nil {
					return e
				}
			} else if i.IsRegular() && strings.HasSuffix(entry.Name(), ".md") {
				files = append(files, path)
			}
		}
		return nil
	}
	if e := walk(dir); e != nil {
		return nil, e
	}
	sort.Strings(files)
	return files, nil
}
func (s *Service) markdown() ([]Definition, []Failure, error) {
	configHome := s.options.ConfigHome
	if s.options.NativeConfigHome != nil {
		configHome = s.options.NativeConfigHome()
	}
	// Source captures userDir at loader entry, before project filesystem work.
	dirs, e := s.projectDirs()
	if e != nil {
		return nil, nil, e
	}
	locked, e := s.locked()
	if e != nil {
		return nil, nil, e
	}
	type root struct{ path, source string }
	roots := []root{{filepath.Join(s.options.ManagedRoot, ".claude/agents"), "policySettings"}}
	if s.enabled("userSettings") && !locked {
		roots = append(roots, root{filepath.Join(configHome, "agents"), "userSettings"})
	}
	if s.enabled("projectSettings") && !locked {
		for _, p := range dirs {
			roots = append(roots, root{p, "projectSettings"})
		}
	}
	all := []Definition{}
	failed := []Failure{}
	seen := map[string]bool{}
	for _, root := range roots {
		files, e := s.find(root.path)
		if e != nil {
			return nil, nil, e
		}
		for _, p := range files {
			raw, e := s.options.Files.ReadFile(p)
			if e != nil {
				var c *CapabilityError
				if errors.As(e, &c) {
					return nil, nil, e
				}
				continue
			}
			front, body, e := Frontmatter(string(protocol.DecodeJavaScriptUTF8(raw)))
			if e != nil {
				return nil, nil, e
			}
			// Source de-duplicates actual files before parse. Identity failure includes.
			info, statError := s.options.Files.Lstat(p)
			identity := ""
			if statError == nil {
				identity = s.options.Files.Identity(info)
			}
			if identity != "" {
				if seen[identity] {
					continue
				}
				seen[identity] = true
			}
			d, why, e := parse(p, root.source, front, body)
			if e != nil {
				return nil, nil, e
			}
			if d == nil {
				if truthy(front.Get("name")) {
					failed = append(failed, Failure{p, why})
				}
				continue
			}
			sum := sha256.Sum256(protocol.DecodeJavaScriptUTF8(raw))
			d.ContentHash = hex.EncodeToString(sum[:])
			d.BaseDir = root.path
			all = append(all, *d)
		}
	}
	return all, failed, nil
}
func parse(path, source string, f protocol.JSONObject, body string) (*Definition, string, error) {
	name, e := exactString(f.Get("name"))
	if e != nil {
		return nil, "", e
	}
	desc, e := exactString(f.Get("description"))
	if e != nil {
		return nil, "", e
	}
	if name == "" {
		return nil, `Missing required "name" field in frontmatter`, nil
	}
	if desc == "" {
		return nil, `Missing required "description" field in frontmatter`, nil
	}
	model := ""
	if raw := f.Get("model"); raw != nil {
		if len(raw) == 0 || raw[0] != '"' || trimJS(f.String("model")) == "" {
			if raw := f.Get("effort"); raw != nil && !effort(raw) {
				return nil, `Invalid "effort" field in frontmatter. Valid options: low, medium, high, xhigh, max or an integer`, nil
			}
			return nil, `Invalid "model" field in frontmatter. Expected a non-empty string or omit the field to inherit`, nil
		}
		model, e = exactString(raw)
		if e != nil {
			return nil, "", e
		}
		model = trimJS(model)
		if strings.ToLower(model) == "inherit" {
			model = "inherit"
		}
	}
	if raw := f.Get("effort"); raw != nil {
		if !effort(raw) {
			return nil, `Invalid "effort" field in frontmatter. Valid options: low, medium, high, xhigh, max or an integer`, nil
		}
	}
	memory := f.String("memory")
	if memory != "user" && memory != "project" && memory != "local" {
		memory = ""
	}
	return &Definition{Name: name, Description: strings.ReplaceAll(desc, `\n`, "\n"), Model: model, Memory: memory, Source: source, FilePath: path, Frontmatter: f, Body: trimJS(body), Effort: effortValue(f.Get("effort"))}, "", nil
}
