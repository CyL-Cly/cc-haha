package clibootstrap

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

// Source's temp directory and bundled-skill nonce are memoized per process,
// including independent agent Owners. They are path producers, not allow rules.
type nativeInternalReadState struct {
	mu      sync.Mutex
	temp    string
	bundled string
}

func (n *NativeBoundary) internalReadState() *nativeInternalReadState {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.internalRead == nil {
		n.internalRead = &nativeInternalReadState{}
	}
	return n.internalRead
}
func (m *MainPrint) nativeClaudeTempRoot(scope *cooperative.Scope) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	state := m.options.Native.internalReadState()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.temp != "" {
		return state.temp, nil
	}
	base := m.options.Native.Environment["CLAUDE_CODE_TMPDIR"]
	if base == "" {
		if m.options.Platform == "windows" {
			base = m.options.Native.Temp
		} else {
			base = "/tmp"
		}
	}
	if canonical, e := filepath.EvalSymlinks(base); e == nil {
		base = canonical
	}
	name := "claude"
	if m.options.Platform != "windows" {
		uid := os.Getuid()
		if uid < 0 {
			uid = 0
		}
		name += "-" + strconv.Itoa(uid)
	}
	state.temp = filepath.Join(base, name) + string(filepath.Separator)
	return state.temp, nil
}
func (m *MainPrint) nativeBundledSkillsRoot(scope *cooperative.Scope) (string, error) {
	temp, e := m.nativeClaudeTempRoot(scope)
	if e != nil {
		return "", e
	}
	state := m.options.Native.internalReadState()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.bundled != "" {
		return state.bundled, nil
	}
	bytes := make([]byte, 16)
	if _, e = rand.Read(bytes); e != nil {
		return "", e
	}
	state.bundled = filepath.Join(temp, "bundled-skills", m.options.Version, hex.EncodeToString(bytes))
	return state.bundled, nil
}
func (m *MainPrint) nativeInternalReadable(scope *cooperative.Scope, path string) (bool, string, error) {
	if m == nil || m.Controller == nil || !scope.BelongsTo(m.Owner) {
		return false, "", cooperative.ErrRestricted
	}
	path = filepath.Clean(path)
	sep := string(filepath.Separator)
	below := func(root string) bool { return strings.HasPrefix(path, filepath.Clean(root)+sep) }
	within := func(root string) bool { return path == filepath.Clean(root) || below(root) }
	env := m.options.Native.Environment
	config := env["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(m.options.Native.Home, ".claude")
	}
	snapshot := m.state.Snapshot()
	project := filepath.Join(config, "projects", projectpath.Sanitize(snapshot.CWD))
	if below(filepath.Join(project, m.Controller.SessionID(), "session-memory")) {
		return true, "Session memory files are allowed for reading", nil
	}
	if within(project) {
		return true, "Project directory files are allowed for reading", nil
	}
	if m.Plans() != nil {
		allowed, e := m.Plans().IsSessionFile(scope, path)
		if e != nil {
			return false, "", e
		}
		if allowed {
			return true, "Plan files for current session are allowed for reading", nil
		}
	}
	tools, e := m.Controller.ToolResultsDirectoryOnOwner(scope)
	if e != nil {
		return false, "", e
	}
	if within(tools) {
		return true, "Tool result files are allowed for reading", nil
	}
	temp, e := m.nativeClaudeTempRoot(scope)
	if e != nil {
		return false, "", e
	}
	projectTemp := filepath.Join(temp, projectpath.Sanitize(snapshot.OriginalCWD))
	enabled, e := m.nativeScratchEnabled(scope)
	if e != nil {
		return false, "", e
	}
	if enabled && within(filepath.Join(projectTemp, m.Controller.SessionID(), "scratchpad")) {
		return true, "Scratchpad files for current session are allowed for reading", nil
	}
	// Source intentionally allows every session of this project, but uses the
	// trailing separator: the project temp root itself is not this carve-out.
	if below(projectTemp) {
		return true, "Project temp directory files are allowed for reading", nil
	}
	base := env["CLAUDE_CODE_REMOTE_MEMORY_DIR"]
	if base == "" {
		base = config
	}
	if below(filepath.Join(base, "agent-memory")) || below(filepath.Join(snapshot.CWD, ".claude", "agent-memory")) {
		return true, "Agent memory files are allowed for reading", nil
	}
	if remote := env["CLAUDE_CODE_REMOTE_MEMORY_DIR"]; remote != "" {
		if below(filepath.Join(remote, "projects")) && strings.Contains(path, sep+"agent-memory-local"+sep) {
			return true, "Agent memory files are allowed for reading", nil
		}
	} else if below(filepath.Join(snapshot.CWD, ".claude", "agent-memory-local")) {
		return true, "Agent memory files are allowed for reading", nil
	}
	if m.memoryPaths != nil {
		directory, e := m.memoryPaths.Directory()
		if e != nil {
			return false, "", e
		}
		if strings.HasPrefix(path, directory) {
			return true, "auto memory files are allowed for reading", nil
		}
	}
	if within(filepath.Join(config, "tasks")) {
		return true, "Task files are allowed for reading", nil
	}
	if within(filepath.Join(config, "teams")) {
		return true, "Team files are allowed for reading", nil
	}
	bundled, e := m.nativeBundledSkillsRoot(scope)
	if e != nil {
		return false, "", e
	}
	if below(bundled) {
		return true, "Bundled skill reference files are allowed for reading", nil
	}
	return false, "", nil
}
