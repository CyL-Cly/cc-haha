package usercontext

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
)

// MemoryPaths resolves the native storage identity shared by system sections
// and instruction loading. It consumes original settings and filesystem state.
type MemoryPaths struct {
	Environment      *clistartupenv.Environment
	Home, ConfigHome string
	// NativeConfigDirectory preserves getClaudeConfigHomeDir's requested path.
	// It is sampled only on a project-root memo miss; finite callers retain
	// their explicit absolute ConfigHome without this native factory binding.
	NativeConfigDirectory func() string
	ProjectRoot           func() string
	SettingsForSource     func(string) (*protocol.JSONObject, error)
	Files                 MemoryFiles
	Realpath              func(string) (string, error)
	mu                    sync.Mutex
	paths                 map[string]string
}

func (p *MemoryPaths) Clear() { p.mu.Lock(); p.paths = nil; p.mu.Unlock() }
func (p *MemoryPaths) Directory() (string, error) {
	if p.Environment == nil || p.ProjectRoot == nil || p.Files == nil || p.Realpath == nil || p.SettingsForSource == nil || !filepath.IsAbs(p.Home) || !filepath.IsAbs(p.ConfigHome) {
		return "", errors.New("auto-memory path requires original environment, settings, project root and native filesystem")
	}
	root := p.ProjectRoot()
	if !filepath.IsAbs(root) {
		return "", errors.New("auto-memory project root must be absolute")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if value, ok := p.paths[root]; ok {
		return value, nil
	}
	value := p.validOverride(rawText(p.Environment.Get("CLAUDE_COWORK_MEMORY_PATH_OVERRIDE")), false)
	if value == "" {
		var setting string
		for _, source := range []string{"policySettings", "flagSettings", "localSettings", "userSettings"} {
			s, err := p.SettingsForSource(source)
			if err != nil {
				return "", err
			}
			if s != nil {
				raw := s.Get("autoMemoryDirectory")
				if raw != nil && string(raw) != "null" {
					setting = rawText(raw)
					break
				}
			}
		}
		value = p.validOverride(setting, true)
	}
	if value == "" {
		base := rawText(p.Environment.Get("CLAUDE_CODE_REMOTE_MEMORY_DIR"))
		if base == "" {
			base = p.ConfigHome
			if p.NativeConfigDirectory != nil {
				base = p.NativeConfigDirectory()
			}
		}
		canonical := canonicalGitRoot(p.Files, p.Realpath, root)
		if canonical == "" {
			canonical = root
		}
		value = norm.NFC.String(filepath.Join(base, "projects", projectpath.Sanitize(canonical), "memory") + string(filepath.Separator))
	}
	if p.paths == nil {
		p.paths = map[string]string{}
	}
	p.paths[root] = value
	return value, nil
}
func (p *MemoryPaths) validOverride(raw string, tilde bool) string {
	if raw == "" || strings.ContainsRune(raw, 0) {
		return ""
	}
	candidate := raw
	if tilde && (strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, "~\\")) {
		rest := filepath.Clean(raw[2:])
		if rest == "." || rest == ".." {
			return ""
		}
		candidate = filepath.Join(p.Home, rest)
	}
	// Source's path.normalize preserves the leading UNC marker; Go Clean does
	// not on Unix, so reject that marker before normalization.
	if strings.HasPrefix(candidate, "\\\\") || strings.HasPrefix(candidate, "//") {
		return ""
	}
	normalized := strings.TrimRight(filepath.Clean(candidate), "/\\")
	if !filepath.IsAbs(normalized) || len(normalized) < 3 || len(normalized) == 2 && normalized[1] == ':' {
		return ""
	}
	return norm.NFC.String(normalized + string(filepath.Separator))
}
func rawText(raw []byte) string {
	u, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return ""
	}
	return stringFromUnits(u)
}
func gitRoot(files MemoryFiles, start string) string {
	for d := filepath.Clean(start); ; d = filepath.Dir(d) {
		if stat, err := files.Stat(filepath.Join(d, ".git")); err == nil && (stat.IsDir() || stat.Mode().IsRegular()) {
			return norm.NFC.String(d)
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return ""
}

// GitRoot exposes the same native .git discovery used by memory identity.
func GitRoot(files MemoryFiles, start string) string { return gitRoot(files, start) }

// CanonicalGitRoot validates the worktree common-dir structure and backlink.
func CanonicalGitRoot(files MemoryFiles, realpath func(string) (string, error), start string) string {
	return canonicalGitRoot(files, realpath, start)
}
func resolveRelative(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}
func canonicalGitRoot(files MemoryFiles, realpath func(string) (string, error), start string) string {
	return ResolveCanonicalGitRoot(files, realpath, gitRoot(files, start))
}

// ResolveCanonicalGitRoot applies the same worktree structure and backlink
// checks to an already discovered git root. Native callers with Source's
// separate discovery/canonical memo lifetimes share this parsing boundary.
func ResolveCanonicalGitRoot(files MemoryFiles, realpath func(string) (string, error), root string) string {
	if root == "" {
		return ""
	}
	raw, err := files.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root
	}
	text := trimMemory(string(raw))
	if !strings.HasPrefix(text, "gitdir:") {
		return root
	}
	worktree := resolveRelative(root, trimMemory(strings.TrimPrefix(text, "gitdir:")))
	common, err := files.ReadFile(filepath.Join(worktree, "commondir"))
	if err != nil {
		return root
	}
	commonDir := resolveRelative(worktree, trimMemory(string(common)))
	if filepath.Dir(worktree) != filepath.Join(commonDir, "worktrees") {
		return root
	}
	back, err := files.ReadFile(filepath.Join(worktree, "gitdir"))
	if err != nil {
		return root
	}
	backlink, err := realpath(trimMemory(string(back)))
	if err != nil {
		return root
	}
	resolvedRoot, err := realpath(root)
	if err != nil || backlink != filepath.Join(resolvedRoot, ".git") {
		return root
	}
	if filepath.Base(commonDir) != ".git" {
		return norm.NFC.String(commonDir)
	}
	return norm.NFC.String(filepath.Dir(commonDir))
}
