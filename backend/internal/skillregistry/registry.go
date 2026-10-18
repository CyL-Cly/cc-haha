// Package skillregistry owns native Skill discovery and desktop projections.
// Runtime command execution and plugin loading are separate shared consumers;
// an actual plugin producer must be bound, never an invented ready state.
package skillregistry

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

type FileSystem interface {
	Stat(string) (fs.FileInfo, error)
	ReadDir(string) ([]fs.DirEntry, error)
	ReadFile(string) ([]byte, error)
	Realpath(string) (string, error)
}
type OSFileSystem struct{}

func (OSFileSystem) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }
func (OSFileSystem) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (OSFileSystem) Realpath(path string) (string, error)  { return filepath.EvalSymlinks(path) }
func (OSFileSystem) ReadDir(path string) ([]fs.DirEntry, error) {
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	return file.ReadDir(-1)
}

type Root struct {
	Path     string `json:"path"`
	Flavor   string `json:"flavor"`
	ScopeKey string `json:"scopeKey"`
}
type Plugin struct {
	Name, SkillsPath string
	SkillsPaths      []string
}
type PluginSource interface {
	EnabledForSkills(context.Context) ([]Plugin, error)
}
type Options struct {
	Home, ConfigDir, CWD, ProjectRoot string
	// NativeConfigDirectory samples the native CLI directory at the original
	// root lookup. Discovery caches keep their own existing lifetimes.
	NativeConfigDirectory func() string
	NativeHomeDirectory   func() string
	FS                    FileSystem
	Environment           func(string) string
	Settings              func(context.Context) (protocol.JSONObject, error)
	Plugins               PluginSource
}
type Service struct{ options Options }
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }
func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.CWD) || !filepath.IsAbs(o.ProjectRoot) || o.Environment == nil || o.Settings == nil || o.Plugins == nil {
		return nil, errors.New("native Skills require filesystem/project/settings and actual plugin discovery bindings")
	}
	if o.FS == nil {
		o.FS = OSFileSystem{}
	}
	return &Service{o}, nil
}
func trimJS(text string) string {
	return strings.TrimFunc(text, func(r rune) bool {
		return r >= '\t' && r <= '\r' || r == ' ' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
	})
}
func inaccessible(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP)
}
func (s *Service) agentsEnabled(ctx context.Context) bool {
	switch strings.ToLower(trimJS(s.options.Environment("CLAUDE_CODE_DISABLE_AGENT_SKILLS_DIR"))) {
	case "1", "true", "yes", "on":
		return false
	}
	settings, err := s.options.Settings(ctx)
	return err != nil || string(settings.Get("disableAgentSkillsDirectory")) != "true"
}
func (s *Service) UserRoots(ctx context.Context) []Root {
	directory := s.options.ConfigDir
	if s.options.NativeConfigDirectory != nil {
		directory = s.options.NativeConfigDirectory()
	}
	roots := []Root{{filepath.Join(directory, "skills"), "claude", "user"}}
	if s.agentsEnabled(ctx) {
		home := s.options.Home
		if s.options.NativeHomeDirectory != nil {
			home = s.options.NativeHomeDirectory()
		}
		roots = append(roots, Root{filepath.Join(norm.NFC.String(home), ".agents", "skills"), "agents", "user"})
	}
	return roots
}
func (s *Service) AddDirRoots(ctx context.Context, dir string) []Root {
	roots := []Root{{filepath.Join(dir, ".claude", "skills"), "claude", "add-dir:" + dir}}
	if s.agentsEnabled(ctx) {
		roots = append(roots, Root{filepath.Join(dir, ".agents", "skills"), "agents", "add-dir:" + dir})
	}
	return roots
}
func (s *Service) NestedCandidates(ctx context.Context, dir string) []string {
	roots := s.AddDirRoots(ctx, dir)
	out := []string{}
	for _, root := range roots {
		out = append(out, root.Path)
	}
	return out
}
func Outranks(claimed, candidate string) bool { return candidate == "claude" && claimed == "agents" }
func normalize(path string) string {
	path = norm.NFC.String(filepath.Clean(path))
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
func (s *Service) gitRoot(start string) string {
	for d := filepath.Clean(start); ; d = filepath.Dir(d) {
		info, err := s.options.FS.Stat(filepath.Join(d, ".git"))
		if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return norm.NFC.String(d)
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}
func (s *Service) canonical(root string) string {
	data, err := s.options.FS.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root
	}
	text := trimJS(string(data))
	if !strings.HasPrefix(text, "gitdir:") {
		return root
	}
	resolve := func(base, path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(base, path)
	}
	dir := resolve(root, trimJS(text[len("gitdir:"):]))
	data, err = s.options.FS.ReadFile(filepath.Join(dir, "commondir"))
	if err != nil {
		return root
	}
	common := resolve(dir, trimJS(string(data)))
	if filepath.Dir(dir) != filepath.Join(common, "worktrees") {
		return root
	}
	data, err = s.options.FS.ReadFile(filepath.Join(dir, "gitdir"))
	if err != nil {
		return root
	}
	back, err := s.options.FS.Realpath(trimJS(string(data)))
	if err != nil {
		return root
	}
	actual, err := s.options.FS.Realpath(root)
	if err != nil || back != filepath.Join(actual, ".git") {
		return root
	}
	if filepath.Base(common) == ".git" {
		return norm.NFC.String(filepath.Dir(common))
	}
	return norm.NFC.String(common)
}
func (s *Service) WalkProjectDirs(cwd string) []string {
	root, session := s.gitRoot(cwd), s.gitRoot(s.options.ProjectRoot)
	if root != "" && session != "" && normalize(s.canonical(root)) != normalize(session) && normalize(root) != normalize(session) && strings.HasPrefix(normalize(root), normalize(session)+string(filepath.Separator)) {
		root = session
	}
	dirs := []string{}
	for d := filepath.Clean(cwd); normalize(d) != normalize(s.options.Home); d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if root != "" && normalize(d) == normalize(root) || d == filepath.Dir(d) {
			break
		}
	}
	return dirs
}
func (s *Service) ProjectRoots(ctx context.Context, cwd string) ([]Root, error) {
	if cwd == "" {
		cwd = s.options.CWD
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(s.options.CWD, cwd)
	}
	flavors := []string{"claude"}
	if s.agentsEnabled(ctx) {
		flavors = append(flavors, "agents")
	}
	roots := []Root{}
	for _, dir := range s.WalkProjectDirs(cwd) {
		for _, flavor := range flavors {
			path := filepath.Join(dir, "."+flavor, "skills")
			_, err := s.options.FS.Stat(path)
			if err == nil {
				roots = append(roots, Root{path, flavor, "project:" + dir})
			} else if !inaccessible(err) {
				return nil, err
			}
		}
	}
	return roots, nil
}

type Meta struct {
	Name          string                     `json:"name"`
	DisplayName   json.RawMessage            `json:"displayName,omitempty"`
	Description   json.RawMessage            `json:"description"`
	Source        string                     `json:"source"`
	RootFlavor    string                     `json:"rootFlavor,omitempty"`
	UserInvocable bool                       `json:"userInvocable"`
	Version       *string                    `json:"version,omitempty"`
	ContentLength int                        `json:"contentLength"`
	HasDirectory  bool                       `json:"hasDirectory"`
	PluginName    string                     `json:"pluginName,omitempty"`
	Root          string                     `json:"-"`
	Parsed        markdownfrontmatter.Parsed `json:"-"`
}

func readText(data []byte) string {
	return string(protocol.DecodeJavaScriptUTF8(data))
}
func (s *Service) loadMeta(dir, name, source, plugin, flavor string) *Meta {
	bytes, err := s.options.FS.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil
	}
	text := readText(bytes)
	parsed, err := markdownfrontmatter.Parse(text)
	if err != nil {
		return nil
	}
	description := parsed.Get("description")
	raw, _ := description.JSON()
	if !description.Truthy() {
		fallback := "No description"
		for _, line := range strings.Split(parsed.Content, "\n") {
			if value := trimJS(line); value != "" {
				fallback = value
				break
			}
		}
		raw, _ = json.Marshal(fallback)
	}
	meta := &Meta{Name: name, Description: raw, Source: source, RootFlavor: flavor, UserInvocable: !(parsed.Get("user-invocable") != nil && parsed.Get("user-invocable").Kind == "boolean" && !parsed.Get("user-invocable").Truthy()), ContentLength: len(utf16.Encode([]rune(text))), HasDirectory: true, PluginName: plugin, Root: dir, Parsed: parsed}
	if value := parsed.Get("name"); value.Truthy() {
		meta.DisplayName, _ = value.JSON()
	}
	if value := parsed.Get("version"); value != nil && value.Kind != "null" {
		text := value.String()
		meta.Version = &text
	}
	return meta
}
func (s *Service) collectRoots(roots []Root, source string) []Meta {
	result := []Meta{}
	seen := map[string]bool{}
	for _, root := range roots {
		entries, err := s.options.FS.ReadDir(root.Path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() && entry.Type()&fs.ModeSymlink == 0 || strings.HasPrefix(entry.Name(), ".") || seen[entry.Name()] {
				continue
			}
			meta := s.loadMeta(filepath.Join(root.Path, entry.Name()), entry.Name(), source, "", root.Flavor)
			if meta == nil {
				continue
			}
			seen[entry.Name()] = true
			result = append(result, *meta)
		}
	}
	return result
}

type location struct{ path, name, plugin string }

func (s *Service) pluginLocations(ctx context.Context) []location {
	plugins, err := s.options.Plugins.EnabledForSkills(ctx)
	if err != nil {
		return []location{}
	}
	result := []location{}
	seen := map[string]bool{}
	add := func(plugin, path string) {
		name := plugin + ":" + filepath.Base(path)
		if !seen[name] {
			seen[name] = true
			result = append(result, location{path, name, plugin})
		}
	}
	for _, plugin := range plugins {
		for _, root := range append([]string{plugin.SkillsPath}, plugin.SkillsPaths...) {
			if root == "" {
				continue
			}
			stat, err := s.options.FS.Stat(filepath.Join(root, "SKILL.md"))
			if err == nil && stat.Mode().IsRegular() {
				add(plugin.Name, root)
				continue
			}
			entries, err := s.options.FS.ReadDir(root)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() && entry.Type()&fs.ModeSymlink == 0 {
					continue
				}
				path := filepath.Join(root, entry.Name())
				stat, err := s.options.FS.Stat(filepath.Join(path, "SKILL.md"))
				if err == nil && stat.Mode().IsRegular() {
					add(plugin.Name, path)
				}
			}
		}
	}
	return result
}
func (s *Service) List(ctx context.Context, cwd string) ([]Meta, error) {
	roots, err := s.ProjectRoots(ctx, cwd)
	if err != nil {
		return nil, err
	}
	list := append(s.collectRoots(s.UserRoots(ctx), "user"), s.collectRoots(roots, "project")...)
	for _, location := range s.pluginLocations(ctx) {
		if meta := s.loadMeta(location.path, location.name, "plugin", location.plugin, ""); meta != nil {
			list = append(list, *meta)
		}
	}
	comparer := collate.New(language.English)
	sort.SliceStable(list, func(i, j int) bool { return comparer.CompareString(list[i].Name, list[j].Name) < 0 })
	return list, nil
}
func (s *Service) UserNames(ctx context.Context) []string {
	names := []string{}
	for _, meta := range s.collectRoots(s.UserRoots(ctx), "user") {
		names = append(names, meta.Name)
	}
	return names
}
func (s *Service) Detail(ctx context.Context, cwd, source, name string) (*Detail, error) {
	if source == "" || name == "" {
		return nil, &Error{400, "Missing required query parameters: source, name"}
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return nil, &Error{400, "Invalid skill name"}
	}
	if source != "user" && source != "project" && source != "plugin" {
		return nil, &Error{400, "Unsupported source: " + source}
	}
	dir, plugin, flavor := "", "", ""
	if source == "plugin" {
		for _, loc := range s.pluginLocations(ctx) {
			if loc.name == name {
				dir, plugin = loc.path, loc.plugin
				break
			}
		}
	} else {
		roots := s.UserRoots(ctx)
		if source == "project" {
			var err error
			roots, err = s.ProjectRoots(ctx, cwd)
			if err != nil {
				return nil, err
			}
		}
		for _, root := range roots {
			path := filepath.Join(root.Path, name)
			stat, err := s.options.FS.Stat(path)
			if err == nil && stat.IsDir() {
				dir, flavor = path, root.Flavor
				break
			}
		}
	}
	if dir == "" {
		return nil, &Error{404, "Skill not found: " + name}
	}
	meta := s.loadMeta(dir, name, source, plugin, flavor)
	if meta == nil {
		return nil, &Error{404, "Skill missing SKILL.md: " + name}
	}
	tree, files := s.fileTree(dir)
	detail := &Detail{Meta: *meta, Tree: tree, Files: files, SkillRoot: dir}
	if source == "user" {
		detail.MarketMeta = s.marketMeta(name)
	}
	return detail, nil
}
