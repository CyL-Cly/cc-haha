package skillcommands

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/ignorepattern"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
)

type Options struct {
	Home, ConfigDir, CWD, ManagedDir string
	Roots                            *skillregistry.Service
	Files                            skillregistry.FileSystem
	Environment                      func(string) string
	EnabledSource                    func(context.Context, string) (bool, error)
	SkillsLocked                     func(context.Context) (bool, error)
	AdditionalDirectories            func(context.Context) []string
	Bare                             func() bool
	ResolveModel                     ModelResolver
	Plugins                          func(context.Context) ([]pluginregistry.Loaded, error)
	InlinePluginCount                func() int
	PluginDataDirectory              func(context.Context, string) (string, error)
	// Gitignored is the actual session's git check-ignore producer. It must
	// honor nested/global ignores and fail open outside repositories.
	Gitignored func(context.Context, string, string) (bool, error)
	OnError    func(error)
}
type Service struct {
	options                    Options
	mu                         sync.Mutex
	cache                      map[string][]Definition
	pending, dynamic           map[string]Definition
	pendingOrder, dynamicOrder []string
	activated, checked         map[string]bool
	listeners                  map[uint64]func()
	nextListener               uint64
	generation                 uint64
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.CWD) || !filepath.IsAbs(o.ManagedDir) || o.Roots == nil || o.Environment == nil || o.EnabledSource == nil || o.SkillsLocked == nil || o.AdditionalDirectories == nil || o.Bare == nil || o.Plugins == nil || o.InlinePluginCount == nil || o.Gitignored == nil {
		return nil, errors.New("native Skill commands require actual roots/settings/policy/plugin/session bindings")
	}
	if o.Files == nil {
		o.Files = skillregistry.OSFileSystem{}
	}
	return &Service{options: o, cache: map[string][]Definition{}, pending: map[string]Definition{}, dynamic: map[string]Definition{}, activated: map[string]bool{}, checked: map[string]bool{}, listeners: map[uint64]func(){}}, nil
}

type entry struct {
	definition          Definition
	path, flavor, scope string
}

func RenderableName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r >= 0x7f && r <= 0x9f {
			return false
		}
	}
	return true
}
func (s *Service) fail(e error) {
	if e != nil && s.options.OnError != nil {
		s.options.OnError(e)
	}
}
func (s *Service) skills(ctx context.Context, root skillregistry.Root, source string) []entry {
	entries, e := s.options.Files.ReadDir(root.Path)
	if e != nil {
		s.fail(e)
		return nil
	}
	out := []entry{}
	for _, child := range entries {
		if !child.IsDir() && child.Type()&fs.ModeSymlink == 0 {
			continue
		}
		if !RenderableName(child.Name()) {
			continue
		}
		path := filepath.Join(root.Path, child.Name(), "SKILL.md")
		raw, e := s.options.Files.ReadFile(path)
		if e != nil {
			if !errors.Is(e, fs.ErrNotExist) {
				s.fail(e)
			}
			continue
		}
		d, e := Parse(ctx, string(protocol.DecodeJavaScriptUTF8(raw)), child.Name(), source, filepath.Dir(path), "skills", s.options.ResolveModel)
		if e != nil {
			s.fail(e)
			continue
		}
		out = append(out, entry{d, path, root.Flavor, root.ScopeKey})
	}
	return out
}
func (s *Service) Local(ctx context.Context, cwd string) ([]Definition, error) {
	if cwd == "" {
		cwd = s.options.CWD
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(s.options.CWD, cwd)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		generation := s.generation
		if data, ok := s.cache[cwd]; ok {
			s.mu.Unlock()
			return append([]Definition{}, data...), nil
		}
		s.mu.Unlock()
		// Settings/plugin callbacks have their own process lock. Never hold the
		// Skill lock while entering them: settings invalidation clears this cache.
		all, bare, err := s.loadLocal(ctx, cwd)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		if generation != s.generation {
			s.mu.Unlock()
			continue
		}
		if data, ok := s.cache[cwd]; ok {
			s.mu.Unlock()
			return append([]Definition{}, data...), nil
		}
		result := []Definition{}
		for _, item := range all {
			d := item.definition
			if !bare && len(d.Paths()) > 0 && !s.activated[d.Name()] {
				if _, ok := s.pending[d.Name()]; !ok {
					s.pendingOrder = append(s.pendingOrder, d.Name())
				}
				s.pending[d.Name()] = d
			} else {
				result = append(result, d)
			}
		}
		s.cache[cwd] = result
		s.mu.Unlock()
		return append([]Definition{}, result...), nil
	}
}

func (s *Service) loadLocal(ctx context.Context, cwd string) ([]entry, bool, error) {
	locked, e := s.options.SkillsLocked(ctx)
	if e != nil {
		return nil, false, e
	}
	project, e := s.options.EnabledSource(ctx, "projectSettings")
	if e != nil {
		return nil, false, e
	}
	project = project && !locked
	dirs := s.options.AdditionalDirectories(ctx)
	all := []entry{}
	add := func() {
		for _, dir := range dirs {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(s.options.CWD, dir)
			}
			for _, root := range s.options.Roots.AddDirRoots(ctx, dir) {
				all = append(all, s.skills(ctx, root, "projectSettings")...)
			}
		}
	}
	bare := s.options.Bare()
	if bare {
		if project {
			add()
		}
	} else {
		if !truthyEnvironment(s.options.Environment("CLAUDE_CODE_DISABLE_POLICY_SKILLS")) {
			all = append(all, s.skills(ctx, skillregistry.Root{Path: filepath.Join(s.options.ManagedDir, ".claude", "skills"), Flavor: "claude", ScopeKey: "managed"}, "policySettings")...)
		}
		user, e := s.options.EnabledSource(ctx, "userSettings")
		if e != nil {
			return nil, false, e
		}
		if user && !locked {
			for _, root := range s.options.Roots.UserRoots(ctx) {
				all = append(all, s.skills(ctx, root, "userSettings")...)
			}
		}
		// Source computes project roots even when the source is disabled.
		roots, e := s.options.Roots.ProjectRoots(ctx, cwd)
		if e != nil {
			return nil, false, e
		}
		if project {
			for _, root := range roots {
				all = append(all, s.skills(ctx, root, "projectSettings")...)
			}
			add()
		}
		if !locked {
			legacy, e := s.legacy(ctx, cwd)
			if e != nil {
				return nil, false, e
			}
			all = append(all, legacy...)
		}
	}
	claimed := map[string]int{}
	seen := map[string]bool{}
	dedup := []entry{}
	for _, item := range all {
		id := ""
		if !bare {
			id, _ = s.options.Files.Realpath(item.path)
			if id != "" && seen[id] {
				continue
			}
		}
		if at, ok := claimed[item.definition.Name()]; ok {
			if !skillregistry.Outranks(dedup[at].flavor, item.flavor) {
				if id != "" {
					seen[id] = true
				}
				continue
			}
			dedup[at] = item
		} else {
			claimed[item.definition.Name()] = len(dedup)
			dedup = append(dedup, item)
		}
		if id != "" {
			seen[id] = true
		}
	}
	return dedup, bare, nil
}
func truthyEnvironment(v string) bool {
	switch strings.ToLower(trimJS(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Clear mirrors clearSkillCaches: it leaves dynamically loaded definitions but
// resets conditional activation, as the original implementation does.
func (s *Service) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.cache = map[string][]Definition{}
	s.pending = map[string]Definition{}
	s.pendingOrder = nil
	s.activated = map[string]bool{}
}
func (s *Service) ClearDynamic() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.checked = map[string]bool{}
	s.dynamic = map[string]Definition{}
	s.dynamicOrder = nil
	s.pending = map[string]Definition{}
	s.pendingOrder = nil
	s.activated = map[string]bool{}
}
func (s *Service) Dynamic() []Definition {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Definition{}
	for _, name := range s.dynamicOrder {
		out = append(out, s.dynamic[name])
	}
	return out
}
func (s *Service) ConditionalCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.pending) }

// ActivateConditional moves matching definitions to the shared dynamic map.
// Pattern semantics use the same pinned ignore language as Source; directory
// discovery and activation are distinct operations with one notification.
func (s *Service) ActivateConditional(paths []string, cwd string) ([]string, error) {
	s.mu.Lock()
	activated := []string{}
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	for _, name := range s.pendingOrder {
		definition, present := s.pending[name]
		if !present {
			continue
		}
		matcher, err := ignorepattern.New(platform)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		for _, pattern := range definition.Paths() {
			if err := matcher.Add(pattern); err != nil {
				s.mu.Unlock()
				return nil, err
			}
		}
		for _, path := range paths {
			relative := path
			if filepath.IsAbs(path) {
				var err error
				relative, err = filepath.Rel(cwd, path)
				if err != nil {
					continue
				}
			}
			if relative == "" || relative == "." || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
				continue
			}
			result, err := matcher.Test(relative)
			if err != nil {
				s.mu.Unlock()
				return nil, err
			}
			if result.Ignored {
				if _, present := s.dynamic[name]; !present {
					s.dynamicOrder = append(s.dynamicOrder, name)
				}
				s.dynamic[name] = definition
				delete(s.pending, name)
				s.activated[name] = true
				activated = append(activated, name)
				break
			}
		}
	}
	s.mu.Unlock()
	if len(activated) > 0 {
		s.emit()
	}
	return activated, nil
}
func (s *Service) OnLoaded(fn func()) func() {
	s.mu.Lock()
	id := s.nextListener
	s.nextListener++
	s.listeners[id] = fn
	s.mu.Unlock()
	return func() { s.mu.Lock(); delete(s.listeners, id); s.mu.Unlock() }
}
func (s *Service) emit() {
	s.mu.Lock()
	fns := []func(){}
	for _, fn := range s.listeners {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.fail(errors.New("dynamic Skill listener panicked"))
				}
			}()
			fn()
		}()
	}
}
func (s *Service) Discover(ctx context.Context, paths []string, cwd string) ([]string, error) {
	cwd = strings.TrimSuffix(cwd, string(filepath.Separator))
	out := []string{}
	for _, path := range paths {
		for dir := filepath.Dir(path); strings.HasPrefix(dir, cwd+string(filepath.Separator)); dir = filepath.Dir(dir) {
			for _, candidate := range s.options.Roots.NestedCandidates(ctx, dir) {
				s.mu.Lock()
				seen := s.checked[candidate]
				s.checked[candidate] = true
				s.mu.Unlock()
				if seen {
					continue
				}
				if _, e := s.options.Files.Stat(candidate); e != nil {
					continue
				}
				ignored, e := s.options.Gitignored(ctx, dir, cwd)
				if e != nil {
					s.fail(e)
					continue
				}
				if !ignored {
					out = append(out, candidate)
				}
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return len(strings.Split(out[i], string(filepath.Separator))) > len(strings.Split(out[j], string(filepath.Separator)))
	})
	return out, nil
}
func (s *Service) AddDirectories(ctx context.Context, dirs []string) error {
	project, e := s.options.EnabledSource(ctx, "projectSettings")
	if e != nil {
		return e
	}
	locked, e := s.options.SkillsLocked(ctx)
	if e != nil {
		return e
	}
	if !project || locked || len(dirs) == 0 {
		return nil
	}
	loaded := make([][]entry, len(dirs))
	for at, dir := range dirs {
		loaded[at] = s.skills(ctx, skillregistry.Root{Path: dir}, "projectSettings")
	}
	s.mu.Lock()
	for at := len(loaded) - 1; at >= 0; at-- {
		for _, item := range loaded[at] {
			name := item.definition.Name()
			if _, ok := s.dynamic[name]; !ok {
				s.dynamicOrder = append(s.dynamicOrder, name)
			}
			s.dynamic[name] = item.definition
		}
	}
	s.mu.Unlock()
	s.emit()
	return nil
}

// NativeFiles provides the ordinary on-disk implementation, with raw readdir
// order retained by skillregistry's shared filesystem adapter.
type NativeFiles struct{ skillregistry.OSFileSystem }

func (NativeFiles) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }

// HasConditional reflects the original conditionalSkills map, rather than the
// visible command/skill catalog (ordinary skills do not enter that map).
func (s *Service) HasConditional() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}
