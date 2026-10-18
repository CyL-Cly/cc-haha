package teamspawn

// InProcessEnabled observes the same launch environment and resolved fallback
// as SelectBackend. The Source footer predicate never probes native backends.
func (p *PaneRuntime) InProcessEnabled(mode string, noninteractive bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if noninteractive || mode == "in-process" {
		return true
	}
	if mode == "tmux" {
		return false
	}
	if p.inProcessFallback {
		return true
	}
	inITerm := p.environment["TERM_PROGRAM"] == "iTerm.app" || p.environment["ITERM_SESSION_ID"] != ""
	return !p.insideTmux && !inITerm
}

// SetMode is the explicit Settings callback. Ordinary file changes retain the
// launch snapshot; this callback changes that snapshot without clearing the
// original cached pane detection or auto-only fallback.
func (p *PaneRuntime) SetMode(mode string) {
	p.mu.Lock()
	p.mode = mode
	p.mu.Unlock()
}
