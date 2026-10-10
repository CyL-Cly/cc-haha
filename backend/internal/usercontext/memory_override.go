package usercontext

// HasOverride shares the original validator with Directory. A malformed env
// value must not remove the normal auto-memory permission carve-out.
func (p *MemoryPaths) HasOverride() bool {
	if p == nil || p.Environment == nil {
		return false
	}
	return p.validOverride(rawText(p.Environment.Get("CLAUDE_COWORK_MEMORY_PATH_OVERRIDE")), false) != ""
}
