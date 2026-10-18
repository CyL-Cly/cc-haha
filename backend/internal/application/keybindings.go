package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/fsnotify/fsnotify"
)

type terminalKeybindingWarning struct {
	Type       string `json:"type"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Key        string `json:"key,omitempty"`
	Context    string `json:"context,omitempty"`
	Action     string `json:"action,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

type terminalKeybindingLoad struct {
	bindings []tui.Keybinding
	warnings []terminalKeybindingWarning
}

type terminalKeybindingListener struct {
	ctx      context.Context
	callback func([]tui.Keybinding)
}

type terminalKeybindingsState struct {
	mu        sync.Mutex
	path      string
	platform  string
	enabled   func() bool
	readFile  func(string) ([]byte, error)
	loaded    bool
	closed    bool
	current   terminalKeybindingLoad
	listeners map[uint64]terminalKeybindingListener
	sequence  uint64
	cancel    context.CancelFunc
	done      chan struct{}
}

func cloneTerminalKeybindings(bindings []tui.Keybinding) []tui.Keybinding {
	copy := make([]tui.Keybinding, len(bindings))
	for i, binding := range bindings {
		binding.Chord = append([]tui.Keystroke(nil), binding.Chord...)
		if binding.Action != nil {
			action := *binding.Action
			binding.Action = &action
		}
		copy[i] = binding
	}
	return copy
}

func (s *Session) terminalKeybindingState() *terminalKeybindingsState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalKeybindings == nil {
		_, path := s.native.TerminalKeybindingsPaths(s.native.CWD)
		platform := s.terminalPlatform
		if platform == "darwin" {
			platform = "macos"
		}
		s.terminalKeybindings = &terminalKeybindingsState{path: path, platform: platform, enabled: s.keybindingCustomizationEnabled, readFile: os.ReadFile, listeners: make(map[uint64]terminalKeybindingListener), closed: s.closed}
	}
	return s.terminalKeybindings
}

func (state *terminalKeybindingsState) defaults() terminalKeybindingLoad {
	return terminalKeybindingLoad{bindings: tui.DefaultKeybindings(state.platform, true, tui.KeybindingFeatures{}), warnings: []terminalKeybindingWarning{}}
}

// Loading and watching share Source's trusted availability boundary. The
// ordinary build has no gate callback and does not inspect keybindings.json.
func (state *terminalKeybindingsState) load(synchronous bool) terminalKeybindingLoad {
	result := state.defaults()
	if state.enabled == nil || !state.enabled() {
		return result
	}
	content, err := state.readFile(state.path)
	if err != nil {
		if !synchronous && !errors.Is(err, os.ErrNotExist) {
			result.warnings = []terminalKeybindingWarning{{Type: "parse_error", Severity: "error", Message: "Failed to parse keybindings.json: " + err.Error()}}
		}
		return result
	}
	parseFailure := func(message, suggestion string) terminalKeybindingLoad {
		result.warnings = []terminalKeybindingWarning{{Type: "parse_error", Severity: "error", Message: message, Suggestion: suggestion}}
		return result
	}
	var wrapper protocol.JSONObject
	if err = wrapper.UnmarshalJSON(content); err != nil {
		if json.Valid(content) {
			return parseFailure(`keybindings.json must have a "bindings" array`, `Use format: { "bindings": [ ... ] }`)
		}
		if !synchronous {
			result.warnings = []terminalKeybindingWarning{{Type: "parse_error", Severity: "error", Message: "Failed to parse keybindings.json: " + err.Error()}}
		}
		return result
	}
	if wrapper.Get("bindings") == nil {
		return parseFailure(`keybindings.json must have a "bindings" array`, `Use format: { "bindings": [ ... ] }`)
	}
	var rawBlocks []json.RawMessage
	if raw := wrapper.Get("bindings"); len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &rawBlocks) != nil {
		return parseFailure(`"bindings" must be an array`, `Set "bindings" to an array of keybinding blocks`)
	}
	blocks := make([]protocol.JSONObject, 0, len(rawBlocks))
	for _, raw := range rawBlocks {
		var block protocol.JSONObject
		var name string
		if block.UnmarshalJSON(raw) != nil || json.Unmarshal(block.Get("context"), &name) != nil || string(block.Get("context")) == "null" {
			return parseFailure("keybindings.json contains invalid block structure", `Each block must have "context" (string) and "bindings" (object)`)
		}
		object, ok := block.Object("bindings")
		if !ok {
			value := block.Get("bindings")
			var array []json.RawMessage
			if len(value) == 0 || value[0] != '[' || json.Unmarshal(value, &array) != nil {
				return parseFailure("keybindings.json contains invalid block structure", `Each block must have "context" (string) and "bindings" (object)`)
			}
			object = protocol.NewJSONObject()
			for i, action := range array {
				object.Set(strconv.Itoa(i), action)
			}
		}
		blocks = append(blocks, protocol.NewJSONObject(field("context", name), field("bindings", object)))
	}
	result.warnings = validateTerminalKeybindings(content, blocks, state.platform)
	// Source keeps invalid non-string actions inert rather than restoring the
	// overridden default. Retain an unknown action token for the Go consumer.
	normalized := make([]protocol.JSONObject, len(blocks))
	for i, block := range blocks {
		object, _ := block.Object("bindings")
		for _, property := range object.OwnProperties() {
			if value := property.Value; len(value) > 0 && value[0] != '"' && string(value) != "null" {
				var name string
				_ = json.Unmarshal(property.NameJSON, &name)
				object.Set(name, raw(string(value)))
			}
		}
		normalized[i] = protocol.NewJSONObject(field("context", block.String("context")), field("bindings", object))
	}
	user, err := tui.ParseBindingBlocks(raw(normalized))
	if err != nil {
		return result
	}
	result.bindings = append(result.bindings, user...)
	return result
}

func (s *Session) currentTerminalKeybindings(ctx context.Context) ([]tui.Keybinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := s.terminalKeybindingState()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return nil, context.Canceled
	}
	if !state.loaded {
		state.current, state.loaded = state.load(true), true
	}
	return cloneTerminalKeybindings(state.current.bindings), nil
}

func (s *Session) KeybindingWarningsSummary() string {
	s.mu.Lock()
	state := s.terminalKeybindings
	s.mu.Unlock()
	if state == nil {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	errors, warnings := 0, 0
	for _, warning := range state.current.warnings {
		if warning.Severity == "error" {
			errors++
		} else if warning.Severity == "warning" {
			warnings++
		}
	}
	plural := func(count int, noun string) string {
		if count == 1 {
			return noun
		}
		return noun + "s"
	}
	message := ""
	if errors > 0 && warnings > 0 {
		message = fmt.Sprintf("Found %d keybinding %s and %d %s", errors, plural(errors, "error"), warnings, plural(warnings, "warning"))
	} else if errors > 0 {
		message = fmt.Sprintf("Found %d keybinding %s", errors, plural(errors, "error"))
	} else if warnings > 0 {
		message = fmt.Sprintf("Found %d keybinding %s", warnings, plural(warnings, "warning"))
	}
	if message != "" {
		return message + " · /doctor for details"
	}
	return ""
}

func (s *Session) SubscribeKeybindings(ctx context.Context, callback func([]tui.Keybinding)) ([]tui.Keybinding, func(), error) {
	initial, err := s.currentTerminalKeybindings(ctx)
	if err != nil {
		return nil, nil, err
	}
	state := s.terminalKeybindingState()
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return nil, nil, context.Canceled
	}
	state.sequence++
	id := state.sequence
	initial = cloneTerminalKeybindings(state.current.bindings)
	state.listeners[id] = terminalKeybindingListener{ctx: ctx, callback: callback}
	state.startWatcherLocked()
	state.mu.Unlock()
	var once sync.Once
	ready := make(chan struct{})
	var stopCancellation func() bool
	remove := func() {
		<-ready
		once.Do(func() { stopCancellation(); state.removeListener(id) })
	}
	stopCancellation = context.AfterFunc(ctx, remove)
	close(ready)
	return initial, remove, nil
}

func (state *terminalKeybindingsState) removeListener(id uint64) {
	state.mu.Lock()
	delete(state.listeners, id)
	cancel, done := state.cancel, state.done
	if len(state.listeners) == 0 {
		state.cancel, state.done, state.loaded = nil, nil, false
	} else {
		cancel, done = nil, nil
	}
	state.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (s *Session) closeTerminalKeybindings() {
	s.mu.Lock()
	state := s.terminalKeybindings
	s.mu.Unlock()
	if state == nil {
		return
	}
	state.mu.Lock()
	state.closed = true
	clear(state.listeners)
	cancel, done := state.cancel, state.done
	state.cancel, state.done = nil, nil
	state.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (state *terminalKeybindingsState) publish(result terminalKeybindingLoad, generation chan struct{}) {
	state.mu.Lock()
	if state.closed || state.done != generation {
		state.mu.Unlock()
		return
	}
	state.current, state.loaded = result, true
	listeners := make([]terminalKeybindingListener, 0, len(state.listeners))
	for _, listener := range state.listeners {
		listeners = append(listeners, listener)
	}
	state.mu.Unlock()
	for _, listener := range listeners {
		if listener.ctx.Err() == nil && listener.callback != nil {
			listener.callback(cloneTerminalKeybindings(result.bindings))
		}
	}
}

type keybindingFileStamp struct {
	exists bool
	size   int64
	time   time.Time
}

func terminalKeybindingStamp(path string) keybindingFileStamp {
	if info, err := os.Stat(path); err == nil {
		return keybindingFileStamp{exists: true, size: info.Size(), time: info.ModTime()}
	}
	return keybindingFileStamp{}
}

func (state *terminalKeybindingsState) startWatcherLocked() {
	if state.cancel != nil || state.enabled == nil || !state.enabled() {
		return
	}
	if info, err := os.Stat(filepath.Dir(state.path)); err != nil || !info.IsDir() {
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	if err = watcher.Add(filepath.Dir(state.path)); err != nil {
		_ = watcher.Close()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	state.cancel, state.done = cancel, done
	go func() {
		defer close(done)
		defer watcher.Close()
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		defer timer.Stop()
		var pending bool
		var since time.Time
		var stamp keybindingFileStamp
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(event.Name) == filepath.Base(state.path) {
					pending, since, stamp = true, time.Now(), terminalKeybindingStamp(state.path)
					timer.Reset(200 * time.Millisecond)
				}
			case <-watcher.Errors:
			case <-timer.C:
				if !pending {
					continue
				}
				current := terminalKeybindingStamp(state.path)
				if !current.exists {
					pending = false
					state.publish(state.defaults(), done)
					continue
				}
				if current != stamp {
					since, stamp = time.Now(), current
				}
				if time.Since(since) < 500*time.Millisecond {
					timer.Reset(200 * time.Millisecond)
					continue
				}
				pending = false
				state.publish(state.load(false), done)
			}
		}
	}()
}
