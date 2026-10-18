package usercontext

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
)

type normalMemoryFuture struct {
	done  chan struct{}
	files []MemoryFile
	err   error
}
type normalFallbackFuture struct {
	done    chan struct{}
	allowed bool
	err     error
}

// ClearMemoryFileCaches differs from ClearUserContext: the original native
// files memo survives a user-context clear, and is reset by explicit lifecycle.
func (m *ExplicitUserMemory) ClearMemoryFileCaches() {
	if m.Normal == nil {
		return
	}
	n := m.Normal
	n.mu.Lock()
	n.cache = nil
	n.fallback = nil
	n.mu.Unlock()
}
func (m *ExplicitUserMemory) ResetMemoryFiles(reason string) {
	if m.Normal == nil {
		return
	}
	n := m.Normal
	n.mu.Lock()
	n.cache = nil
	n.fallback = nil
	n.nextLoadReason = reason
	n.fireInstructions = true
	n.initialized = true
	n.mu.Unlock()
}
func (m *ExplicitUserMemory) NormalMemoryFiles(mode string, additional []string, forceExternal bool) ([]MemoryFile, error) {
	n := m.Normal
	if n == nil || m.Files == nil || !filepath.IsAbs(n.CWD) || !filepath.IsAbs(n.ManagedRoot) || n.ReadDir == nil {
		return nil, &CapabilityError{"normal memory original native bindings"}
	}
	switch mode {
	case "claude-md", "claude-md-or-agents-md", "claude-md-and-agents-md", "managed-only":
	default:
		return nil, &CapabilityError{"normal memory instruction mode"}
	}
	if len(n.Excludes) > 0 && n.ExcludeMatcher == nil && n.Matcher == nil {
		return nil, &CapabilityError{"normal memory original exclude matcher"}
	}
	n.mu.Lock()
	if !n.initialized {
		n.initialized = true
		n.nextLoadReason = "session_start"
		n.fireInstructions = true
	}
	if n.cacheMode != mode {
		n.cacheMode = mode
		n.cache = nil
		n.fallback = nil
	}
	if n.cache == nil {
		n.cache = map[bool]*normalMemoryFuture{}
	}
	if f, exists := n.cache[forceExternal]; exists {
		n.mu.Unlock()
		if err := m.waitMemory(f.done); err != nil {
			return nil, err
		}
		return append([]MemoryFile(nil), f.files...), f.err
	}
	f := &normalMemoryFuture{done: make(chan struct{})}
	n.cache[forceExternal] = f
	n.mu.Unlock()
	f.files, f.err = m.discoverNormal(mode, additional, forceExternal)
	if files, ok := m.Files.(NativeMemoryPrimitives); ok && f.err == nil {
		f.err = files.MemoryError()
	}
	reason := ""
	if f.err == nil && !forceExternal {
		n.mu.Lock()
		if n.fireInstructions {
			reason = n.nextLoadReason
			n.fireInstructions = false
		}
		n.mu.Unlock()
	}
	if f.err == nil && reason != "" && n.InstructionsLoaded != nil {
		for _, file := range f.files {
			if file.Type != "Managed" && file.Type != "User" && file.Type != "Project" && file.Type != "Local" {
				continue
			}
			r := reason
			if file.Parent != "" {
				r = "include"
			}
			n.InstructionsLoaded(file, r)
		}
	}
	close(f.done)
	return append([]MemoryFile(nil), f.files...), f.err
}
func (m *ExplicitUserMemory) loadNativeNormal(mode string, additional []string) (json.RawMessage, error) {
	files, err := m.NormalMemoryFiles(mode, additional, false)
	if err != nil {
		return nil, err
	}
	blocks := []json.RawMessage{}
	for _, file := range files {
		if m.Normal.SkipMemoryIndex != nil && m.Normal.SkipMemoryIndex() && (file.Type == "AutoMem" || file.Type == "TeamMem") {
			continue
		}
		if m.Normal.SkipProjectLevel != nil && m.Normal.SkipProjectLevel() && (file.Type == "Project" || file.Type == "Local") {
			continue
		}
		if file.Content == "" {
			continue
		}
		description := " (user's private global instructions for all projects)"
		switch file.Type {
		case "Project":
			description = " (project instructions, checked into the codebase)"
		case "Local":
			description = " (user's private project instructions, not checked in)"
		case "AutoMem":
			description = " (user's auto-memory, persists across conversations)"
		}
		units, decodeError := protocol.DecodeJavaScriptJSONString(file.PromptContent())
		if decodeError != nil {
			return nil, decodeError
		}
		space := func(unit uint16) bool { return trimMemory(string(rune(unit))) == "" }
		for len(units) > 0 && space(units[0]) {
			units = units[1:]
		}
		for len(units) > 0 && space(units[len(units)-1]) {
			units = units[:len(units)-1]
		}
		blocks = append(blocks, concat(quote("Contents of "+file.Path+description+":\n\n"), protocol.QuoteJavaScriptUTF16(units)))
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	parts := []json.RawMessage{quote(memoryInstructionPrompt + "\n\n")}
	for i, block := range blocks {
		if i > 0 {
			parts = append(parts, quote("\n\n"))
		}
		parts = append(parts, block)
	}
	return concat(parts...), nil
}
func normalizeMemoryPath(path string) string {
	path = norm.NFC.String(filepath.Clean(path))
	if len(path) >= 2 && path[1] == ':' {
		path = strings.ToLower(path[:1]) + path[1:]
	}
	return path
}
func withinMemoryPath(path, root string) bool {
	path, root = normalizeMemoryPath(path), normalizeMemoryPath(root)
	aliases := func(value string) string {
		if strings.HasPrefix(value, "/private/var/") {
			value = "/var/" + strings.TrimPrefix(value, "/private/var/")
		}
		if value == "/private/tmp" || strings.HasPrefix(value, "/private/tmp/") {
			value = strings.TrimPrefix(value, "/private")
		}
		if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			value = strings.ToLower(value)
		}
		return value
	}
	path, root = aliases(path), aliases(root)
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
func (m *ExplicitUserMemory) nativeRealpath(path string) string {
	if result, err := m.nativeResolvePath(path); err == nil {
		return result
	}
	return path
}
func (m *ExplicitUserMemory) processNormalFile(path, kind string, processed map[string]bool, external bool, depth int, parent string) []MemoryFile {
	normalized := normalizeMemoryPath(path)
	if processed[normalized] || depth >= 5 {
		return nil
	}
	n := m.Normal
	if (kind == "User" || kind == "Project" || kind == "Local") && len(n.Excludes) > 0 {
		if n.Matcher != nil {
			match, err := n.Matcher.IsExcluded(path, n.Excludes)
			if err != nil {
				panic(excludeFailure{err})
			}
			if match {
				return nil
			}
		} else if n.ExcludeMatcher(path, n.Excludes) {
			return nil
		}
	}
	resolved := m.nativeRealpath(path)
	processed[normalized] = true
	if resolved != path {
		processed[normalizeMemoryPath(resolved)] = true
	}
	raw, err := m.Files.ReadFile(path)
	if err != nil {
		return nil
	}
	// Node's native UTF-8 reader replaces malformed sequences instead of
	// rejecting an entire instruction file.
	decoded := string(protocol.DecodeJavaScriptUTF8(raw))
	home := n.Home
	if home == "" {
		home = filepath.Dir(m.ConfigHome)
	}
	file, includes := parseMemory(decoded, path, kind, resolved, home)
	if file == nil || trimMemory(file.Content) == "" {
		return nil
	}
	file.Parent = parent
	result := []MemoryFile{*file}
	for _, include := range includes {
		if !external && !withinMemoryPath(include, n.CWD) {
			continue
		}
		result = append(result, m.processNormalFile(include, kind, processed, external, depth+1, path)...)
	}
	return result
}
func (m *ExplicitUserMemory) normalRules(directory, kind string, processed, visited map[string]bool, external bool) (result []MemoryFile) {
	defer func() {
		if failure := recover(); failure != nil {
			if _, ok := failure.(excludeFailure); ok {
				result = nil
			} else {
				panic(failure)
			}
		}
	}()
	if visited[directory] {
		return nil
	}
	resolved := m.nativeRealpath(directory)
	visited[directory] = true
	if resolved != directory {
		visited[resolved] = true
	}
	entries, err := m.nativeReadDir(resolved)
	if err != nil {
		return nil
	}
	result = []MemoryFile{}
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		native := m.nativeRealpath(path)
		isDir, isFile := entry.IsDir(), entry.Type().IsRegular()
		if native != path || entry.Type()&fs.ModeSymlink != 0 {
			stat, err := m.Files.Stat(native)
			if err != nil {
				return nil
			}
			isDir, isFile = stat.IsDir(), stat.Mode().IsRegular()
		}
		if isDir {
			result = append(result, m.normalRules(native, kind, processed, visited, external)...)
		} else if isFile && strings.HasSuffix(entry.Name(), ".md") {
			for _, file := range m.processNormalFile(native, kind, processed, external, 0, "") {
				if len(file.Globs) == 0 {
					result = append(result, file)
				}
			}
		}
	}
	return result
}

type normalDirectory struct {
	path        string
	skipProject bool
}

func (m *ExplicitUserMemory) normalDirectories() []normalDirectory {
	n := m.Normal
	resolve := m.nativeResolvePath
	files := m.nativeSynchronousFiles()
	git, canonical := gitRoot(files, n.CWD), canonicalGitRoot(files, resolve, n.CWD)
	nested := git != "" && canonical != "" && normalizeMemoryPath(git) != normalizeMemoryPath(canonical) && withinMemoryPath(git, canonical)
	dirs := []normalDirectory{}
	for d := n.CWD; filepath.Dir(d) != d; d = filepath.Dir(d) {
		dirs = append([]normalDirectory{{path: d, skipProject: nested && withinMemoryPath(d, canonical) && !withinMemoryPath(d, git)}}, dirs...)
	}
	return dirs
}
func (m *ExplicitUserMemory) normalHasSource(name string) bool {
	for _, source := range m.Normal.EnabledSources {
		if source == name {
			return true
		}
	}
	return false
}
func (m *ExplicitUserMemory) hasNormalClaude(directory normalDirectory, explicit bool) bool {
	candidates := []struct{ path, kind string }{}
	if !directory.skipProject && (explicit || m.normalHasSource("projectSettings")) {
		candidates = append(candidates, struct{ path, kind string }{filepath.Join(directory.path, "CLAUDE.md"), "Project"}, struct{ path, kind string }{filepath.Join(directory.path, ".claude", "CLAUDE.md"), "Project"})
	}
	if !explicit && m.normalHasSource("localSettings") {
		candidates = append(candidates, struct{ path, kind string }{filepath.Join(directory.path, "CLAUDE.local.md"), "Local"})
	}
	for _, candidate := range candidates {
		if len(m.processNormalFile(candidate.path, candidate.kind, map[string]bool{}, false, 0, "")) > 0 {
			return true
		}
	}
	return false
}

func (m *ExplicitUserMemory) canUseNormalFallback(dirs []normalDirectory) (allowed bool) {
	n := m.Normal
	n.mu.Lock()
	if n.fallback == nil {
		n.fallback = map[string]*normalFallbackFuture{}
	}
	if f := n.fallback[n.CWD]; f != nil {
		n.mu.Unlock()
		if err := m.waitMemory(f.done); err != nil {
			panic(excludeFailure{err})
		}
		if f.err != nil {
			panic(excludeFailure{f.err})
		}
		return f.allowed
	}
	f := &normalFallbackFuture{done: make(chan struct{})}
	n.fallback[n.CWD] = f
	n.mu.Unlock()
	defer func() {
		if failure := recover(); failure != nil {
			if excluded, ok := failure.(excludeFailure); ok {
				f.err = excluded.err
				close(f.done)
				panic(failure)
			}
			close(f.done)
			panic(failure)
		}
		f.allowed = allowed
		close(f.done)
	}()
	for _, directory := range dirs {
		if m.hasNormalClaude(directory, false) {
			return false
		}
	}
	return true
}
func (m *ExplicitUserMemory) discoverNormal(mode string, additional []string, forceExternal bool) (files []MemoryFile, failure error) {
	defer func() {
		if value := recover(); value != nil {
			if excluded, ok := value.(excludeFailure); ok {
				files = nil
				failure = excluded.err
			} else {
				panic(value)
			}
		}
	}()
	n := m.Normal
	dirs := m.normalDirectories()
	agents := mode == "claude-md-and-agents-md"
	if mode == "claude-md-or-agents-md" {
		agents = m.canUseNormalFallback(dirs)
	}
	processed := map[string]bool{}
	n.mu.Lock()
	external := forceExternal || n.IncludeExternal
	n.mu.Unlock()
	result := []MemoryFile{}
	addFile := func(path, kind string, allowExternal bool) {
		result = append(result, m.processNormalFile(path, kind, processed, allowExternal, 0, "")...)
	}
	addRules := func(path, kind string, allowExternal bool) {
		result = append(result, m.normalRules(path, kind, processed, map[string]bool{}, allowExternal)...)
	}
	addProject := func(directory string) {
		addFile(filepath.Join(directory, "CLAUDE.md"), "Project", external)
		addFile(filepath.Join(directory, ".claude", "CLAUDE.md"), "Project", external)
		addRules(filepath.Join(directory, ".claude", "rules"), "Project", external)
	}
	addAgents := func(directory string) {
		addFile(filepath.Join(directory, "AGENTS.md"), "Project", external)
		addFile(filepath.Join(directory, ".claude", "AGENTS.md"), "Project", external)
	}
	addFile(filepath.Join(n.ManagedRoot, "CLAUDE.md"), "Managed", external)
	addRules(filepath.Join(n.ManagedRoot, ".claude", "rules"), "Managed", external)
	managedOnly := mode == "managed-only"
	if !managedOnly && m.normalHasSource("userSettings") {
		addFile(filepath.Join(m.normalUserConfigDirectory(), "CLAUDE.md"), "User", true)
		addRules(filepath.Join(m.normalUserConfigDirectory(), "rules"), "User", true)
	}
	for _, directory := range dirs {
		if !managedOnly && m.normalHasSource("projectSettings") && !directory.skipProject {
			addProject(directory.path)
		}
		if !managedOnly && m.normalHasSource("localSettings") {
			addFile(filepath.Join(directory.path, "CLAUDE.local.md"), "Local", external)
		}
		if !managedOnly && !directory.skipProject && m.normalHasSource("projectSettings") && agents {
			addAgents(directory.path)
		}
	}
	if !managedOnly && n.AdditionalDirectoriesEnabled {
		for _, directory := range additional {
			addProject(directory)
			if mode == "claude-md-and-agents-md" || mode == "claude-md-or-agents-md" && !m.hasNormalClaude(normalDirectory{path: directory}, true) {
				addAgents(directory)
			}
		}
	}
	if n.AutoMemoryEnabled {
		directory := ""
		if native, ok := m.Files.(NativeMemoryPrimitives); ok {
			var err error
			directory, err = native.MemoryDirectory()
			if err != nil {
				return nil, err
			}
		} else if n.AutoMemoryDirectory != nil {
			var err error
			directory, err = n.AutoMemoryDirectory()
			if err != nil {
				return nil, err
			}
		} else {
			resolve := n.Realpath
			if resolve == nil {
				resolve = filepath.EvalSymlinks
			}
			base := canonicalGitRoot(m.Files, resolve, n.CWD)
			if base == "" {
				base = n.CWD
			}
			directory = filepath.Join(m.ConfigHome, "projects", projectpath.Sanitize(base), "memory")
		}
		path := filepath.Join(directory, "MEMORY.md")
		if raw, err := m.Files.ReadFile(path); err == nil {
			file, _ := parseMemory(string(protocol.DecodeJavaScriptUTF8(raw)), path, "AutoMem", "", "")
			if file != nil && !processed[normalizeMemoryPath(path)] {
				processed[normalizeMemoryPath(path)] = true
				result = append(result, *file)
			}
		}
	}
	return deduplicateNormalAgents(result), nil
}

func (m *ExplicitUserMemory) normalUserConfigDirectory() string {
	if m.Normal.NativeProcess && m.Normal.NativeConfigDirectory != nil {
		return m.Normal.NativeConfigDirectory()
	}
	return m.ConfigHome
}

func deduplicateNormalAgents(files []MemoryFile) []MemoryFile {
	byPath := map[string]MemoryFile{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	fromAgents := func(file MemoryFile) bool {
		seen := map[string]bool{}
		for file.Parent != "" && !seen[file.Path] {
			parent, exists := byPath[file.Parent]
			if !exists {
				break
			}
			seen[file.Path] = true
			file = parent
		}
		return file.Type == "Project" && filepath.Base(file.Path) == "AGENTS.md"
	}
	contents := map[string]bool{}
	for _, file := range files {
		if !fromAgents(file) && (file.Type == "Project" || file.Type == "Local") {
			contents[trimMemory(file.Content)] = true
		}
	}
	result := []MemoryFile{}
	for _, file := range files {
		if fromAgents(file) {
			content := trimMemory(file.Content)
			if content != "" && contents[content] {
				continue
			}
			if content != "" {
				contents[content] = true
			}
		}
		result = append(result, file)
	}
	return result
}

// NativeReadDir keeps readdir's original directory order. os.ReadDir sorts its
// result, which changes rule precedence compared with the original native fs.
func NativeReadDir(path string) ([]fs.DirEntry, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return directory.ReadDir(-1)
}
