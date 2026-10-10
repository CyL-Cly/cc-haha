package usercontext

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf16"
)

const NormalEmptyMemoryProfile = "source-normal-empty-memory-v1"

type NormalMemoryDiscovery struct {
	CWD, ManagedRoot             string
	EnabledSources               []string
	AutoMemoryEnabled            bool
	NativeProcess                bool
	ReadDir                      func(string) ([]fs.DirEntry, error)
	Realpath                     func(string) (string, error)
	Home                         string
	AdditionalDirectoriesEnabled bool
	IncludeExternal              bool
	AutoMemoryDirectory          func() (string, error)
	Excludes                     []string
	ExcludeMatcher               func(string, []string) bool
	Matcher                      *ExclusionMatcher
	SkipMemoryIndex              func() bool
	SkipProjectLevel             func() bool
	InstructionsLoaded           func(MemoryFile, string)
	mu                           sync.Mutex
	cacheMode                    string
	cache                        map[bool]*normalMemoryFuture
	fallback                     map[string]*normalFallbackFuture
	nextLoadReason               string
	fireInstructions             bool
	initialized                  bool
}

// getMemoryFiles always checks managed instructions/rules; user/project/local
// discovery obeys setting-source selection. Empty native files yield no text.
// Active files require markdown/hooks capabilities and are never erased.
func (m *ExplicitUserMemory) loadNormal(mode string, additional []string) (json.RawMessage, error) {
	if m.Files == nil || !filepath.IsAbs(m.ConfigHome) {
		return nil, &CapabilityError{"normal memory host/mode"}
	}
	n := m.Normal
	if n == nil {
		return nil, &CapabilityError{"normal memory native discovery binding"}
	}
	if n.NativeProcess {
		return m.loadNativeNormal(mode, additional)
	}
	if len(additional) > 0 {
		return nil, &CapabilityError{"normal memory host/mode"}
	}
	if !filepath.IsAbs(n.CWD) || !filepath.IsAbs(n.ManagedRoot) || n.ReadDir == nil {
		return nil, &CapabilityError{"normal memory original paths/readDir"}
	}
	// Source defaults to claude-md-or-agents-md. With no enabled project/local
	// sources, canUseAgentsFallback has no CLAUDE candidates and the actual
	// processAgentsFiles call is disabled by projectSettings. The mode remains
	// unchanged; we admit precisely this real empty-source branch.
	if mode != "claude-md" {
		if mode != "claude-md-or-agents-md" {
			return nil, &CapabilityError{"normal memory instruction mode"}
		}
		for _, source := range n.EnabledSources {
			if source == "projectSettings" || source == "localSettings" {
				if !n.NativeProcess {
					return nil, &CapabilityError{"normal project-wide AGENTS fallback"}
				}
			}
		}
	}
	has := func(name string) bool {
		for _, s := range n.EnabledSources {
			if s == name {
				return true
			}
		}
		return false
	}
	file := func(path string) error {
		b, e := m.Files.ReadFile(path)
		if e != nil {
			return nil
		}
		if strings.TrimSpace(string(b)) != "" {
			return &CapabilityError{"active memory markdown " + path}
		}
		return nil
	}
	rules := func(path string) error {
		entries, e := n.ReadDir(path)
		if e != nil {
			// Source processMdRules catches every native discovery error at its outer boundary.
			return nil
		}
		for _, f := range entries {
			if f.IsDir() || strings.HasSuffix(f.Name(), ".md") || f.Type()&fs.ModeSymlink != 0 {
				return &CapabilityError{"active rules discovery " + path}
			}
		}
		return nil
	}
	if e := file(filepath.Join(n.ManagedRoot, "CLAUDE.md")); e != nil {
		return nil, e
	}
	if e := rules(filepath.Join(n.ManagedRoot, ".claude", "rules")); e != nil {
		return nil, e
	}
	if has("userSettings") {
		if e := file(filepath.Join(m.ConfigHome, "CLAUDE.md")); e != nil {
			return nil, e
		}
		if e := rules(filepath.Join(m.ConfigHome, "rules")); e != nil {
			return nil, e
		}
	}
	dirs := []string{}
	for d := n.CWD; filepath.Dir(d) != d; d = filepath.Dir(d) {
		dirs = append([]string{d}, dirs...)
		if filepath.Dir(d) == d {
			break
		}
	}
	for _, d := range dirs {
		if has("projectSettings") {
			if n.NativeProcess && mode == "claude-md-or-agents-md" {
				// Read the real fallback candidates. Empty files are harmless;
				// active instruction parsing remains an explicit boundary.
				if e := file(filepath.Join(d, "AGENTS.md")); e != nil {
					return nil, e
				}
			}
			for _, p := range []string{filepath.Join(d, "CLAUDE.md"), filepath.Join(d, ".claude", "CLAUDE.md")} {
				if e := file(p); e != nil {
					return nil, e
				}
			}
			if e := rules(filepath.Join(d, ".claude", "rules")); e != nil {
				return nil, e
			}
		}
		if has("localSettings") {
			if e := file(filepath.Join(d, "CLAUDE.local.md")); e != nil {
				return nil, e
			}
		}
	}
	if n.AutoMemoryEnabled { // Source falls back to projectRoot for a non-repository.
		for d := n.CWD; ; d = filepath.Dir(d) {
			if _, e := m.Files.Stat(filepath.Join(d, ".git")); e == nil {
				return nil, &CapabilityError{"canonical git memory root"}
			}
			if filepath.Dir(d) == d {
				break
			}
		}
		key := []byte{}
		for _, c := range utf16.Encode([]rune(n.CWD)) {
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
				key = append(key, byte(c))
			} else {
				key = append(key, '-')
			}
		}
		if e := file(filepath.Join(m.ConfigHome, "projects", string(key), "memory", "MEMORY.md")); e != nil {
			return nil, e
		}
	}
	return nil, nil
}
