package systemprompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"golang.org/x/text/unicode/norm"
)

type StyleConfig struct {
	OutputStyle
	Source         string
	Description    string
	ForceForPlugin bool
	Nil            bool
}
type StyleLoaderOptions struct {
	Environment                             *clistartupenv.Environment
	Platform, Home, ConfigHome, ManagedRoot string
	Files                                   usercontext.MemoryFiles
	ReadDir                                 func(string) ([]fs.DirEntry, error)
	Realpath                                func(string) (string, error)
	Lstat                                   func(string) (fs.FileInfo, error)
	ProjectRoot                             func() string
	SourceEnabled                           func(string) bool
	Settings                                func() (protocol.JSONObject, error)
	PluginStyles                            func(context.Context) ([]StyleConfig, error)
}
type styleFuture struct {
	done   chan struct{}
	values []StyleConfig
	err    error
}
type StyleLoader struct {
	options          StyleLoaderOptions
	mu               sync.Mutex
	all, directories map[string]*styleFuture
}

func NewStyleLoader(o StyleLoaderOptions) (*StyleLoader, error) {
	if o.Environment == nil || o.Files == nil || o.ReadDir == nil || o.Realpath == nil || o.Lstat == nil || o.ProjectRoot == nil || o.SourceEnabled == nil || o.Settings == nil || o.PluginStyles == nil || !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.ConfigHome) || !filepath.IsAbs(o.ManagedRoot) {
		return nil, errors.New("output styles require original native paths/files, settings/source state and plugin loader")
	}
	return &StyleLoader{options: o, all: map[string]*styleFuture{}, directories: map[string]*styleFuture{}}, nil
}
func (l *StyleLoader) ClearAll() { l.mu.Lock(); l.all = map[string]*styleFuture{}; l.mu.Unlock() }

func (l *StyleLoader) ClearDirectories() {
	l.mu.Lock()
	l.directories = map[string]*styleFuture{}
	l.mu.Unlock()
}

// List shares the exact resolver cache and precedence with Agent prompts.
// Callers receive an independent slice so settings views cannot change selection.
func (l *StyleLoader) List(ctx context.Context, cwd string) ([]StyleConfig, error) {
	values, err := l.loadAll(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return append([]StyleConfig(nil), values...), nil
}
func (l *StyleLoader) Resolve(ctx context.Context, cwd string) (*OutputStyle, error) {
	return l.resolveWithOptions(ctx, cwd, l.options, nil)
}
func (l *StyleLoader) resolveWithOptions(ctx context.Context, cwd string, o StyleLoaderOptions, wait func(<-chan struct{}) error) (*OutputStyle, error) {
	all, err := l.loadAllWithOptions(ctx, cwd, o, wait)
	if err != nil {
		return nil, err
	}
	for _, style := range all {
		if style.Source == "plugin" && style.ForceForPlugin {
			value := style.OutputStyle
			return &value, nil
		}
	}
	settings, err := o.Settings()
	if err != nil {
		return nil, err
	}
	name := rawString(settings.Get("outputStyle"))
	if name == "" {
		name = "default"
	}
	for _, style := range all {
		if style.Name == name {
			if style.Nil {
				return nil, nil
			}
			value := style.OutputStyle
			return &value, nil
		}
	}
	return nil, nil
}
func (l *StyleLoader) loadAll(ctx context.Context, cwd string) ([]StyleConfig, error) {
	return l.loadAllWithOptions(ctx, cwd, l.options, nil)
}
func (l *StyleLoader) loadAllWithOptions(ctx context.Context, cwd string, o StyleLoaderOptions, wait func(<-chan struct{}) error) ([]StyleConfig, error) {
	l.mu.Lock()
	if f := l.all[cwd]; f != nil {
		l.mu.Unlock()
		if wait != nil {
			if err := wait(f.done); err != nil {
				return nil, err
			}
		} else {
			<-f.done
		}
		return f.values, f.err
	}
	f := &styleFuture{done: make(chan struct{})}
	l.all[cwd] = f
	l.mu.Unlock()
	defer close(f.done)
	custom, err := l.loadDirectoriesWithOptions(cwd, o, wait)
	if native, ok := o.Files.(usercontext.NativeMemoryPrimitives); ok {
		if err := native.MemoryError(); err != nil {
			f.err = err
			return nil, err
		}
	}
	if err != nil {
		// Source getOutputStyleDirStyles catches discovery failures and returns [].
		custom = nil
	}
	plugins, err := o.PluginStyles(ctx)
	if err != nil {
		f.err = err
		return nil, err
	}
	if native, ok := o.Files.(usercontext.NativeMemoryPrimitives); ok {
		if err := native.MemoryError(); err != nil {
			f.err = err
			return nil, err
		}
	}
	values := []StyleConfig{{OutputStyle: OutputStyle{Name: "default"}, Source: "built-in", Nil: true}, {OutputStyle: OutputStyle{Name: "Explanatory", Prompt: strings.ReplaceAll(explanatoryStylePrompt, "__FIGURES_STAR__", l.star()), KeepCodingInstructions: true}, Source: "built-in", Description: "Claude explains its implementation choices and codebase patterns"}, {OutputStyle: OutputStyle{Name: "Learning", Prompt: strings.ReplaceAll(learningStylePrompt, "__FIGURES_STAR__", l.star()), KeepCodingInstructions: true}, Source: "built-in", Description: "Claude pauses and asks you to write small pieces of code for hands-on practice"}}
	indices := map[string]int{"default": 0, "Explanatory": 1, "Learning": 2}
	groups := [][]StyleConfig{plugins}
	for _, source := range []string{"userSettings", "projectSettings", "policySettings"} {
		group := []StyleConfig{}
		for _, style := range custom {
			if style.Source == source {
				group = append(group, style)
			}
		}
		groups = append(groups, group)
	}
	for _, group := range groups {
		for _, style := range group {
			if i, exists := indices[style.Name]; exists {
				values[i] = style
			} else {
				indices[style.Name] = len(values)
				values = append(values, style)
			}
		}
	}
	// Object.values enumerates integer property names before insertion-ordered
	// names. Plugin-forced selection observes that order.
	sort.SliceStable(values, func(i, j int) bool {
		a, ae := styleIndex(values[i].Name)
		b, be := styleIndex(values[j].Name)
		if ae && be {
			return a < b
		}
		return ae && !be
	})
	f.values = values
	return values, nil
}
func styleIndex(name string) (uint64, bool) {
	var v uint64
	if name == "" || len(name) > 1 && name[0] == '0' {
		return 0, false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + uint64(c-'0')
		if v >= 4294967295 {
			return 0, false
		}
	}
	return v, true
}
func (l *StyleLoader) star() string {
	o := l.options
	e := func(name string) string { return rawString(o.Environment.Get(name)) }
	supported := e("TERM") != "linux"
	if o.Platform == "win32" {
		supported = e("WT_SESSION") != "" || e("TERMINUS_SUBLIME") != "" || e("ConEmuTask") == "{cmd::Cmder}" || e("TERM_PROGRAM") == "Terminus-Sublime" || e("TERM_PROGRAM") == "vscode" || e("TERM") == "xterm-256color" || e("TERM") == "alacritty" || e("TERM") == "rxvt-unicode" || e("TERM") == "rxvt-unicode-256color" || e("TERMINAL_EMULATOR") == "JetBrains-JediTerm"
	}
	if supported {
		return "★"
	}
	return "✶"
}
func stylePath(path string) string {
	path = norm.NFC.String(filepath.Clean(path))
	if len(path) >= 2 && path[1] == ':' {
		path = strings.ToLower(path[:1]) + path[1:]
	}
	return path
}
func pathDescendant(path, root string) bool {
	p, r := stylePath(path), stylePath(root)
	return p != r && strings.HasPrefix(p, r+string(filepath.Separator))
}
func (l *StyleLoader) projectDirectories(cwd string) ([]string, error) {
	return l.projectDirectoriesWithOptions(cwd, l.options)
}
func (l *StyleLoader) projectDirectoriesWithOptions(cwd string, o StyleLoaderOptions) ([]string, error) {
	if native, ok := o.Files.(usercontext.NativeMemoryPrimitives); ok {
		o.Files, o.Realpath = native.SyncMemoryFiles(), native.SyncMemoryRealpath
	}
	git := usercontext.GitRoot(o.Files, cwd)
	session := usercontext.GitRoot(o.Files, o.ProjectRoot())
	canonical := usercontext.CanonicalGitRoot(o.Files, o.Realpath, cwd)
	stop := git
	if git != "" && session != "" && canonical != "" && stylePath(canonical) != stylePath(session) && pathDescendant(git, session) {
		stop = session
	}
	dirs := []string{}
	for current := filepath.Clean(cwd); ; current = filepath.Dir(current) {
		if stylePath(current) == stylePath(o.Home) {
			break
		}
		path := filepath.Join(current, ".claude", "output-styles")
		if _, err := o.Files.Stat(path); err == nil {
			dirs = append(dirs, path)
		} else if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
			return nil, err
		}
		if stop != "" && stylePath(current) == stylePath(stop) || filepath.Dir(current) == current {
			break
		}
	}
	if git != "" && canonical != "" && canonical != git {
		target := stylePath(filepath.Join(git, ".claude", "output-styles"))
		exists := false
		for _, directory := range dirs {
			exists = exists || stylePath(directory) == target
		}
		if !exists {
			main := filepath.Join(canonical, ".claude", "output-styles")
			already := false
			for _, directory := range dirs {
				already = already || directory == main
			}
			if !already {
				dirs = append(dirs, main)
			}
		}
	}
	return dirs, nil
}
func (l *StyleLoader) loadDirectories(cwd string) ([]StyleConfig, error) {
	return l.loadDirectoriesWithOptions(cwd, l.options, nil)
}
func (l *StyleLoader) loadDirectoriesWithOptions(cwd string, o StyleLoaderOptions, wait func(<-chan struct{}) error) ([]StyleConfig, error) {
	l.mu.Lock()
	if f := l.directories[cwd]; f != nil {
		l.mu.Unlock()
		if wait != nil {
			if err := wait(f.done); err != nil {
				return nil, err
			}
		} else {
			<-f.done
		}
		return f.values, f.err
	}
	f := &styleFuture{done: make(chan struct{})}
	l.directories[cwd] = f
	l.mu.Unlock()
	defer close(f.done)
	project, err := l.projectDirectoriesWithOptions(cwd, o)
	if err != nil {
		f.err = err
		return nil, err
	}
	type directory struct{ path, source string }
	directories := []directory{{filepath.Join(o.ManagedRoot, ".claude", "output-styles"), "policySettings"}}
	if o.SourceEnabled("userSettings") {
		directories = append(directories, directory{filepath.Join(o.ConfigHome, "output-styles"), "userSettings"})
	}
	if o.SourceEnabled("projectSettings") {
		for _, path := range project {
			directories = append(directories, directory{path, "projectSettings"})
		}
	}
	seen := map[string]bool{}
	for _, dir := range directories {
		for _, path := range l.markdownPathsWithOptions(dir.path, o) {
			if native, ok := o.Files.(usercontext.NativeMemoryPrimitives); ok {
				if err := native.MemoryError(); err != nil {
					f.err = err
					return nil, err
				}
			}
			identity := ""
			if stat, err := o.Lstat(path); err == nil {
				identity = styleFileIdentity(stat)
			}
			if identity != "" && seen[identity] {
				continue
			}
			if identity != "" {
				seen[identity] = true
			}
			raw, err := o.Files.ReadFile(path)
			if err != nil {
				continue
			}
			fields, content := usercontext.ParseMarkdownFrontmatter(string(protocol.DecodeJavaScriptUTF8(raw)))
			name := strings.TrimSuffix(filepath.Base(path), ".md")
			if original := fields["name"]; styleTruthy(original) {
				b, err := json.Marshal(original)
				if err != nil {
					continue
				}
				text, err := protocol.ExactJavaScriptStringJSON(b)
				if err != nil {
					continue
				}
				name = rawString(text)
			}
			keep := fields["keep-coding-instructions"] == true || fields["keep-coding-instructions"] == "true"
			f.values = append(f.values, StyleConfig{OutputStyle: OutputStyle{Name: name, Prompt: styleTrim(content), KeepCodingInstructions: keep}, Source: dir.source, Description: styleDescription(fields["description"], content, strings.TrimSuffix(filepath.Base(path), ".md"))})
		}
	}
	if native, ok := o.Files.(usercontext.NativeMemoryPrimitives); ok {
		if err := native.MemoryError(); err != nil {
			f.err = err
			return nil, err
		}
	}
	return f.values, nil
}
func (l *StyleLoader) markdownPaths(directory string) []string {
	return l.markdownPathsWithOptions(directory, l.options)
}
func (l *StyleLoader) markdownPathsWithOptions(directory string, o StyleLoaderOptions) []string {
	paths := []string{}
	visited := map[string]bool{}
	var walk func(string)
	walk = func(path string) {
		stat, err := o.Files.Stat(path)
		if err != nil || !stat.IsDir() {
			return
		}
		identity := styleFileIdentity(stat)
		if identity == "" {
			identity, err = o.Realpath(path)
			if err != nil {
				return
			}
		}
		if visited[identity] {
			return
		}
		visited[identity] = true
		entries, err := o.ReadDir(path)
		if err != nil {
			return
		}
		for _, entry := range entries {
			leaf := filepath.Join(path, entry.Name())
			isDir, isFile := entry.IsDir(), entry.Type().IsRegular()
			if entry.Type()&fs.ModeSymlink != 0 {
				stat, err := o.Files.Stat(leaf)
				if err != nil {
					continue
				}
				isDir, isFile = stat.IsDir(), stat.Mode().IsRegular()
			}
			if isDir {
				walk(leaf)
			} else if isFile && strings.HasSuffix(entry.Name(), ".md") {
				paths = append(paths, leaf)
			}
		}
	}
	walk(directory)
	sort.Strings(paths)
	return paths
}
func styleFileIdentity(stat fs.FileInfo) string {
	if stat.Sys() == nil {
		return ""
	}
	v := reflect.ValueOf(stat.Sys())
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	dev, ino := v.FieldByName("Dev"), v.FieldByName("Ino")
	if !dev.IsValid() || !ino.IsValid() || !dev.CanUint() || !ino.CanUint() {
		return ""
	}
	if dev.Uint() == 0 && ino.Uint() == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", dev.Uint(), ino.Uint())
}
func styleTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= 9 && r <= 13 || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}

func styleTruthy(value any) bool {
	if value == nil || value == false || value == "" {
		return false
	}
	switch number := value.(type) {
	case int:
		return number != 0
	case int64:
		return number != 0
	case uint64:
		return number != 0
	case float64:
		return number != 0
	}
	return true
}
func styleDescription(value any, content, fileName string) string {
	if text, ok := value.(string); ok {
		if text = styleTrim(text); text != "" {
			return text
		}
	} else {
		switch value.(type) {
		case bool, int, int64, uint64, float64:
			raw, err := json.Marshal(value)
			if err == nil {
				if text, err := protocol.ExactJavaScriptStringJSON(raw); err == nil {
					return rawString(text)
				}
			}
		}
	}
	for _, line := range strings.Split(content, "\n") {
		text := styleTrim(line)
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "#") {
			marks := 0
			for marks < len(text) && text[marks] == '#' {
				marks++
			}
			suffix := text[marks:]
			// ECMAScript \s and trim have the same WhiteSpace/LineTerminator set.
			trimmed := strings.TrimLeftFunc(suffix, func(r rune) bool { return styleTrim(string(r)) == "" })
			if trimmed != suffix && trimmed != "" {
				text = trimmed
			}
		}
		units := utf16.Encode([]rune(text))
		if len(units) > 100 {
			text = string(utf16.Decode(units[:97])) + "..."
		}
		return text
	}
	return "Custom " + fileName + " output style"
}
