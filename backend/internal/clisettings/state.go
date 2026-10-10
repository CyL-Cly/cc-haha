// Package clisettings implements the CLI settings boundary. Its state follows
// settingsCache.ts/internalWrites.ts; desktop settings uses a separate service.
package clisettings

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ParsedSettings struct {
	Settings       *protocol.JSONObject
	Errors         []json.RawMessage
	errorReference *[]json.RawMessage
}

// State is retained by the CLI runtime across settings reads and writes, not
// allocated per session/request. Cache values keep their existing reference
// identity. Cloning validated parse results belongs to the parser boundary.
type State struct {
	mu        sync.Mutex
	now       func() float64
	session   *ParsedSettings
	perSource map[string]*protocol.JSONObject
	parsed    map[string]*ParsedSettings
	plugin    *protocol.JSONObject
	writes    map[string]float64
}

func NewState(now func() float64) (*State, error) {
	if now == nil {
		return nil, errors.New("CLI settings state requires an explicit clock")
	}
	return &State{now: now, perSource: map[string]*protocol.JSONObject{}, parsed: map[string]*ParsedSettings{}, writes: map[string]float64{}}, nil
}

func (state *State) GetSessionSettingsCache() *ParsedSettings {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.session
}

func (state *State) SetSessionSettingsCache(value *ParsedSettings) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.session = value
}

// The bool distinguishes undefined (cache miss) from cached null settings.
func (state *State) GetCachedSettingsForSource(source string) (*protocol.JSONObject, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	value, exists := state.perSource[source]
	return value, exists
}

func (state *State) SetCachedSettingsForSource(source string, value *protocol.JSONObject) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.perSource[source] = value
}

func (state *State) GetCachedParsedFile(path string) *ParsedSettings {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.parsed[path]
}

func (state *State) SetCachedParsedFile(path string, value *ParsedSettings) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.parsed[path] = value
}

func (state *State) ResetSettingsCache() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.session = nil
	clear(state.perSource)
	clear(state.parsed)
}

func (state *State) GetPluginSettingsBase() *protocol.JSONObject {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.plugin
}

func (state *State) SetPluginSettingsBase(value *protocol.JSONObject) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.plugin = value
}

func (state *State) ClearPluginSettingsBase() { state.SetPluginSettingsBase(nil) }

func (state *State) MarkInternalWrite(path string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.writes[path] = state.now()
}

func (state *State) ConsumeInternalWrite(path string, windowMS float64) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	timestamp, exists := state.writes[path]
	if exists && state.now()-timestamp < windowMS {
		delete(state.writes, path)
		return true
	}
	return false
}

func (state *State) ClearInternalWrites() {
	state.mu.Lock()
	defer state.mu.Unlock()
	clear(state.writes)
}
