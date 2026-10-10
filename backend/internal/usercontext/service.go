// Package usercontext retains the release user/date memo at the original ASK
// boundary. It is independent of request preparation and retry attempts.
package usercontext

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const ExternalProfile = "external-user-context-release-no-policy-no-active-plugins"

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "user context capability is not migrated: " + e.Boundary
}

type Options struct {
	Environment   *clistartupenv.Environment
	Settings      *clisettings.Service
	SettingsState *clisettings.State
	Now           func() time.Time
	NowMillis     func() float64
	Diagnostics   *diagnostics.Process
	Profile       string
	// FlagSettings is the original inline/file configuration, not resolved mode.
	FlagSettings          *protocol.JSONObject
	BareArgument          bool
	AdditionalDirectories []string
	Memory                *ExplicitUserMemory
}

type Future struct {
	done               chan struct{}
	value              *protocol.JSONObject
	err                error
	diagnosticStart    float64
	diagnosticDisabled bool
}

// Await cancellation only stops this waiter. Source cache clear/caller abort
// does not cancel an already running shared memory producer.
func (f *Future) Await(ctx context.Context) (*protocol.JSONObject, error) {
	if ctx == nil {
		return nil, errors.New("user context wait requires caller context")
	}
	select {
	case <-f.done:
		return f.value, f.err
	default:
	}
	select {
	case <-f.done:
		return f.value, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f *Future) Pending() bool {
	select {
	case <-f.done:
		return false
	default:
		return true
	}
}

type Service struct {
	mu           sync.Mutex
	options      Options
	mode         string
	future       *Future
	sessionSet   bool
	session      json.RawMessage
	injection    json.RawMessage
	cachedMemory json.RawMessage
}

func New(options Options) (*Service, error) {
	if options.Environment == nil || options.Settings == nil || options.SettingsState == nil || options.Now == nil {
		return nil, errors.New("user context requires original environment, settings/cache and clock")
	}
	if options.Profile != ExternalProfile {
		return nil, &CapabilityError{"host policy/active plugin profile"}
	}
	if options.SettingsState.GetPluginSettingsBase() != nil {
		return nil, &CapabilityError{"plugin base contradicts absent profile"}
	}
	if options.NowMillis == nil {
		// Date.now is independent of the injected calendar/new Date primitive.
		options.NowMillis = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	return &Service{options: options, cachedMemory: json.RawMessage("null")}, nil
}
func (s *Service) GetLocalISODate() json.RawMessage {
	raw := s.options.Environment.Get("CLAUDE_CODE_OVERRIDE_DATE")
	if truthy(raw) {
		return append(json.RawMessage(nil), raw...)
	}
	now := s.options.Now()
	return quote(localDate(now))
}
func (s *Service) GetSessionStartDate() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sessionSet {
		s.session = s.GetLocalISODate()
		s.sessionSet = true
	}
	return append(json.RawMessage(nil), s.session...)
}
func (s *Service) ClearSessionStartDate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionSet = false
	s.session = nil
}

// SetFlagSettings mirrors the original bootstrap setter: it does not reset
// settings caches. The caller performs the actual separate cache lifecycle.
func (s *Service) SetFlagSettings(value *protocol.JSONObject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.FlagSettings = value
}
func (s *Service) ClearUserContext() { s.mu.Lock(); defer s.mu.Unlock(); s.future = nil }
func (s *Service) SetSystemPromptInjection(value json.RawMessage) error {
	if value != nil && string(value) != "null" {
		if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
			return &CapabilityError{"non-string system prompt injection"}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.injection = append(json.RawMessage(nil), value...)
	s.future = nil
	return nil
}
func (s *Service) SystemPromptInjection() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.injection...)
}
func (s *Service) CachedClaudeMd() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.cachedMemory...)
}
func (s *Service) CachedMode() (string, *Future) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode, s.future
}

// GetUserContext executes the resolver on every call before looking up the
// cache, just like lodash memoize's resolver. Returning to A after B replaces
// the old A promise; a reset of settings alone does not clear same-mode memo.
func (s *Service) GetUserContext() *Future {
	mode, err := s.InstructionFilesMode()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		f := &Future{done: make(chan struct{}), err: err}
		close(f.done)
		return f
	}
	if mode != s.mode {
		s.mode = mode
		s.future = nil
	}
	if s.future != nil {
		return s.future
	}

	start := s.options.NowMillis()
	f := &Future{done: make(chan struct{}), diagnosticStart: start}
	s.future = f
	if s.options.Diagnostics != nil {
		if err := s.options.Diagnostics.Log("info", "user_context_started", nil); err != nil {
			f.err = err
			close(f.done)
			return f
		}
	}
	disabled, err := envTruthy(s.options.Environment.Get("CLAUDE_CODE_DISABLE_CLAUDE_MDS"))
	if err == nil && !disabled {
		bare := s.options.BareArgument
		if !bare {
			bare, err = envTruthy(s.options.Environment.Get("CLAUDE_CODE_SIMPLE"))
		}
		disabled = bare && len(s.options.AdditionalDirectories) == 0
	}
	if err != nil {
		f.err = err
		close(f.done)
		return f
	}
	f.diagnosticDisabled = disabled
	if disabled {
		s.finishLocked(f, nil, nil)
		return f
	}
	if s.options.Memory == nil {
		f.err = &CapabilityError{"memory discovery/producer"}
		close(f.done)
		return f
	}
	memoryProducer := s.options.Memory
	directories := append([]string{}, s.options.AdditionalDirectories...)
	go func() {
		memory, err := memoryProducer.load(mode, directories)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.finishLocked(f, memory, err)
	}()
	return f
}
func (s *Service) finishLocked(f *Future, memory json.RawMessage, err error) {
	if err != nil {
		f.err = err
		close(f.done)
		return
	}
	s.cachedMemory = json.RawMessage("null")
	result := protocol.NewJSONObject()
	if truthy(memory) {
		result.Set("claudeMd", memory)
		s.cachedMemory = append(json.RawMessage(nil), memory...)
	}

	length := 0
	if memory != nil && string(memory) != "null" {
		units, decodeErr := protocol.DecodeJavaScriptJSONString(memory)
		if decodeErr != nil {
			f.err = decodeErr
			close(f.done)
			return
		}
		length = len(units)
	}
	duration := s.options.NowMillis() - f.diagnosticStart
	if s.options.Diagnostics != nil {
		disabled := json.RawMessage("false")
		if f.diagnosticDisabled {
			disabled = json.RawMessage("true")
		}
		data := protocol.NewJSONObject(
			protocol.JSONField{Name: "duration_ms", Value: diagnostics.Number(duration)},
			protocol.JSONField{Name: "claudemd_length", Value: diagnostics.Number(float64(length))},
			protocol.JSONField{Name: "claudemd_disabled", Value: disabled},
		)
		if err := s.options.Diagnostics.Log("info", "user_context_completed", &data); err != nil {
			f.err = err
			close(f.done)
			return
		}
	}
	date, err := protocol.ExactJavaScriptStringJSON(s.GetLocalISODate())
	if err != nil {
		f.err = err
		close(f.done)
		return
	}
	result.Set("currentDate", concat(quote("Today's date is "), date, quote(".")))
	f.value = &result
	close(f.done)
}

// SharesSettings checks immutable context/cache/environment construction binding.
func (s *Service) SharesSettings(settings *clisettings.Service, state *clisettings.State, environment *clistartupenv.Environment) bool {
	return s != nil && settings != nil && state != nil && environment != nil && s.options.Settings == settings && s.options.SettingsState == state && s.options.Environment == environment && settings.UsesState(state)
}

// SetWorkingDirectory retains the session/date memo while clearing the Source
// CWD-dependent memory caches. The user-context mode memo is retained. In-flight old producers keep
// their own discovery descriptor, rather than observing a half-updated CWD.
func (s *Service) SetWorkingDirectory(cwd string) error {
	if !filepath.IsAbs(cwd) {
		return &CapabilityError{"actual working directory"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	memory := s.options.Memory
	if memory != nil && memory.Normal != nil {
		old := memory.Normal
		old.mu.Lock()
		reason, fire, initialized := old.nextLoadReason, old.fireInstructions, old.initialized
		old.mu.Unlock()
		fresh := &NormalMemoryDiscovery{CWD: cwd, ManagedRoot: old.ManagedRoot, EnabledSources: append([]string{}, old.EnabledSources...), AutoMemoryEnabled: old.AutoMemoryEnabled, NativeProcess: old.NativeProcess, ReadDir: old.ReadDir, Realpath: old.Realpath, Home: old.Home, AdditionalDirectoriesEnabled: old.AdditionalDirectoriesEnabled, IncludeExternal: old.IncludeExternal, AutoMemoryDirectory: old.AutoMemoryDirectory, Excludes: append([]string{}, old.Excludes...), ExcludeMatcher: old.ExcludeMatcher, Matcher: old.Matcher, SkipMemoryIndex: old.SkipMemoryIndex, SkipProjectLevel: old.SkipProjectLevel, InstructionsLoaded: old.InstructionsLoaded}
		fresh.nextLoadReason = reason
		fresh.fireInstructions = fire
		fresh.initialized = initialized
		s.options.Memory = &ExplicitUserMemory{ConfigHome: memory.ConfigHome, Profile: memory.Profile, Normal: fresh, Files: memory.Files}
	}
	return nil
}
