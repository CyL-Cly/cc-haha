package clibootstrap

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (m *MainPrint) nativeScratchEnabled(scope *cooperative.Scope) (bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return false, cooperative.ErrRestricted
	}
	env := m.options.Native.Environment
	if env["USER_TYPE"] == "ant" {
		var overrides protocol.JSONObject
		if json.Unmarshal([]byte(env["CLAUDE_INTERNAL_FC_OVERRIDES"]), &overrides) == nil && overrides.IsObject() {
			if value := overrides.Get("tengu_scratch"); value != nil {
				return planRawTruthy(value), nil
			}
		}
		global, e := m.Global.Get()
		if e == nil && global != nil {
			if values, ok := global.Object("growthBookOverrides"); ok {
				if value := values.Get("tengu_scratch"); value != nil {
					return planRawTruthy(value), nil
				}
			}
		}
	}
	// The actual external Source GrowthBook key is empty. Its original
	// cached getter exits before reading cachedStatsigGates/GrowthBook payloads.
	return false, nil
}
func (m *MainPrint) nativeScratchpadPath(scope *cooperative.Scope) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	env := m.options.Native.Environment
	base := env["CLAUDE_CODE_TMPDIR"]
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
		name += "-" + strconv.Itoa(os.Getuid())
	}
	state := m.state.Snapshot()
	return filepath.Join(base, name, projectpath.Sanitize(state.OriginalCWD), m.Controller.SessionID(), "scratchpad"), nil
}

// The real native paths are fetched lazily in Source's original order. No
// synthetic defaults or second state store may authorize internal edits.
func (m *MainPrint) nativeHookInternalEditable(scope *cooperative.Scope, path string) (bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return false, cooperative.ErrRestricted
	}
	path = filepath.Clean(path)
	if m.Plans() != nil {
		allowed, e := m.Plans().IsSessionFile(scope, path)
		if e != nil {
			return false, e
		}
		if allowed {
			return true, nil
		}
	}
	enabled, e := m.nativeScratchEnabled(scope)
	if e != nil {
		return false, e
	}
	if enabled {
		scratch, e := m.nativeScratchpadPath(scope)
		if e != nil {
			return false, e
		}
		if path == scratch || strings.HasPrefix(path, scratch+string(filepath.Separator)) {
			return true, nil
		}
	}
	env := m.options.Native.Environment
	state := m.state.Snapshot()
	config := m.options.Native.ConfigHomeDirectory()
	if config == "" {
		config = filepath.Join(m.options.Native.Home, ".claude")
	}
	memoryBase := env["CLAUDE_CODE_REMOTE_MEMORY_DIR"]
	if memoryBase == "" {
		memoryBase = config
	}
	within := func(root string) bool { return strings.HasPrefix(path, filepath.Join(root)+string(filepath.Separator)) }
	if within(filepath.Join(memoryBase, "agent-memory")) || within(filepath.Join(state.CWD, ".claude", "agent-memory")) {
		return true, nil
	}
	if remote := env["CLAUDE_CODE_REMOTE_MEMORY_DIR"]; remote != "" {
		if within(filepath.Join(remote, "projects")) && strings.Contains(path, string(filepath.Separator)+"agent-memory-local"+string(filepath.Separator)) {
			return true, nil
		}
	} else if within(filepath.Join(state.CWD, ".claude", "agent-memory-local")) {
		return true, nil
	}
	if m.memoryPaths != nil && !m.memoryPaths.HasOverride() {
		directory, e := m.memoryPaths.Directory()
		if e != nil {
			return false, e
		}
		if strings.HasPrefix(path, directory) {
			return true, nil
		}
	}
	// Source preview launch config is case-insensitive, and belongs to the
	// current original project rather than the user's global .claude directory.
	return strings.EqualFold(path, filepath.Join(state.OriginalCWD, ".claude", "launch.json")), nil
}
