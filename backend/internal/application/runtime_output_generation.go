package application

import (
	"encoding/json"
)

// A provider/model restart replaces the native Host while keeping DesktopSession
// and its socket. A retired Host's idle/progress must not describe its successor.
func (d *DesktopSession) nextRuntimeOutput() func(json.RawMessage) error {
	d.mu.Lock()
	d.outputGeneration++
	generation := d.outputGeneration
	running := d.cliRunning
	d.cliRunning = false
	d.mu.Unlock()
	if running {
		// MAIN endReplacedCliRun clears the run marker even when the retiring
		// Host's own idle event is lost with its previous SDK token.
		_ = d.output(json.RawMessage(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
	}
	return func(data json.RawMessage) error { return d.outputForGeneration(data, generation) }
}

func (d *DesktopSession) RuntimeAlive() bool {
	d.mu.Lock()
	closed, engine, replacing := d.closed, d.engine, d.runtimeTransition != nil
	d.mu.Unlock()
	if replacing && !closed {
		return true
	}
	if closed || engine == nil {
		return false
	}
	engine.mu.Lock()
	closed = engine.closed
	engine.mu.Unlock()
	return !closed
}

func (d *DesktopSession) RuntimeTransitionPending() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runtimeTransition
}

func (d *DesktopSession) beginRuntimeTransition() func() {
	d.mu.Lock()
	ready := make(chan struct{})
	d.runtimeTransition = ready
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		if d.runtimeTransition == ready {
			d.runtimeTransition = nil
		}
		close(ready)
		d.mu.Unlock()
	}
}
