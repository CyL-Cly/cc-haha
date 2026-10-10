package clisettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const ExternalReleaseProfile = "cli-settings-external-transcript-classifier-v1"

type CapabilityError struct{ Capability string }

func (e *CapabilityError) Error() string {
	return "unsupported CLI settings capability: " + e.Capability
}

type Options struct {
	State                            *State
	ConfigHome, OriginalCWD, Profile string
	Now                              func() float64
	PID                              int
	FS                               FileSystem
	Observe                          func(Effect)
	Diagnostic                       func(json.RawMessage)
}
type Effect struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Source  string `json:"source,omitempty"`
	Target  string `json:"target,omitempty"`
	Bytes   string `json:"bytes,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
	Options any    `json:"options,omitempty"`
}
type Service struct {
	mu      sync.Mutex
	options Options
}
type Patch struct {
	Object         protocol.JSONObject
	UndefinedPaths [][]json.RawMessage
}
type WriteResult struct{ Error error }

// NewService requires the resolved host profile. It does not initialize CLI,
// query merged managed settings, enable credentials, or infer host policy.
func NewService(options Options) (*Service, error) {
	if options.State == nil || options.Now == nil || options.PID <= 0 || !filepath.IsAbs(options.ConfigHome) || !filepath.IsAbs(options.OriginalCWD) {
		return nil, errors.New("CLI settings requires explicit state, paths, PID and clock")
	}
	if options.Profile != ExternalReleaseProfile && options.Profile != NativeProcessProfile {
		return nil, &CapabilityError{"schema host profile"}
	}
	if options.FS == nil {
		options.FS = OSFileSystem{}
	}
	return &Service{options: options}, nil
}
func (s *Service) emit(e Effect) {
	if s.options.Observe != nil {
		s.options.Observe(e)
	}
}
func cloneParsed(value *ParsedSettings) *ParsedSettings {
	var settings *protocol.JSONObject
	if value.Settings != nil {
		clone := value.Settings.Clone()
		settings = &clone
	}
	ref := value.errorReference
	if ref == nil {
		ref = &value.Errors
		value.errorReference = ref
	}
	return &ParsedSettings{Settings: settings, Errors: *ref, errorReference: ref}
}

// ErrorReference exposes the shared error array explicitly. Appending through
// this pointer retains the Source errors-array identity across cloned reads.
func (value *ParsedSettings) ErrorReference() *[]json.RawMessage {
	if value.errorReference == nil {
		value.errorReference = &value.Errors
	}
	return value.errorReference
}
func (s *Service) ParseFile(path string) (*ParsedSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parseFile(path)
}
func (s *Service) parseFile(path string) (*ParsedSettings, error) {
	if cached := s.options.State.GetCachedParsedFile(path); cached != nil {
		if cached.Settings != nil {
			if err := s.checkCapabilities(*cached.Settings, true); err != nil {
				return nil, err
			}
			if err := checkMergeValues(*cached.Settings); err != nil {
				return nil, err
			}
		}
		return cloneParsed(cached), nil
	}
	result := &ParsedSettings{Errors: []json.RawMessage{}}
	resolved, err := resolveReadPath(s.options.FS, path)
	if err != nil {
		return nil, err
	}
	content, err := readContent(s.options.FS, resolved)
	if err == nil {
		if trimJS(content) == "" {
			o := protocol.NewJSONObject()
			result.Settings = &o
		} else {
			result, err = s.ValidateJSONContent(content, path)
			var capability *CapabilityError
			if errors.As(err, &capability) {
				return nil, err
			}
		}
	} else {
		var capability *CapabilityError
		if errors.As(err, &capability) {
			return nil, err
		}
	}
	s.options.State.SetCachedParsedFile(path, result)
	return cloneParsed(result), nil
}
func (s *Service) Path(source, projectOverride string) (string, error) {
	switch source {
	case "userSettings":
		return filepath.Join(s.options.ConfigHome, "settings.json"), nil
	case "projectSettings":
		root := s.options.OriginalCWD
		if projectOverride != "" {
			root = projectOverride
		}
		if !filepath.IsAbs(root) {
			return "", &CapabilityError{"unresolved project path"}
		}
		return filepath.Join(root, ".claude", "settings.json"), nil
	case "localSettings":
		root := s.options.OriginalCWD
		if projectOverride != "" {
			root = projectOverride
		}
		if !filepath.IsAbs(root) {
			return "", &CapabilityError{"unresolved project path"}
		}
		return filepath.Join(root, ".claude", "settings.local.json"), nil
	default:
		return "", &CapabilityError{"settings source " + source}
	}
}
func (s *Service) GetEditableSource(source, projectOverride string) (*protocol.JSONObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if projectOverride == "" {
		if cached, ok := s.options.State.GetCachedSettingsForSource(source); ok {
			if cached != nil {
				if err := s.checkCapabilities(*cached, true); err != nil {
					return nil, err
				}
				if err := checkMergeValues(*cached); err != nil {
					return nil, err
				}
			}
			return cached, nil
		}
	}
	path, err := s.Path(source, projectOverride)
	if err != nil {
		return nil, err
	}
	parsed, err := s.parseFile(path)
	if err != nil {
		return nil, err
	}
	if projectOverride == "" {
		s.options.State.SetCachedSettingsForSource(source, parsed.Settings)
	}
	return parsed.Settings, nil
}
func (s *Service) UpdateEditableSource(source, projectOverride string, patch Patch) (WriteResult, error) {
	return s.updateEditableSource(source, projectOverride, patch, nil)
}
func (s *Service) updateEditableSource(source, projectOverride string, patch Patch, native *NativeSourceWriterBindings) (WriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// These are the actual Source read-only exits, not a default noop handler.
	if source == "policySettings" || source == "flagSettings" {
		return WriteResult{}, nil
	}
	path, err := s.Path(source, projectOverride)
	if err != nil {
		return WriteResult{}, err
	}
	// Source's read/path parser does not discover Git. Its local write later
	// starts addFileGlobRuleToGitignore without awaiting; that native business is
	// not represented here. Refuse that write capability before partial effects.
	if source == "localSettings" && native == nil {
		root := s.options.OriginalCWD
		if projectOverride != "" {
			root = projectOverride
		}
		for current := filepath.Clean(root); ; current = filepath.Dir(current) {
			if info, e := s.options.FS.Stat(filepath.Join(current, ".git")); e == nil && (info.IsDir() || info.Mode().IsRegular()) {
				return WriteResult{}, &CapabilityError{"local settings write Git-ignore continuation"}
			}
			if filepath.Dir(current) == current {
				break
			}
		}
	}
	if !patch.Object.IsObject() {
		return WriteResult{}, &CapabilityError{"patch requires ordered object"}
	}
	if err = s.checkCapabilities(patch.Object, false); err != nil {
		return WriteResult{}, err
	}
	if err = validateStatusLinePatch(patch.Object); err != nil {
		return WriteResult{}, err
	}
	if err = checkMergeValues(patch.Object); err != nil {
		return WriteResult{}, err
	}
	incoming := patch.Object.Clone()
	for _, parts := range patch.UndefinedPaths {
		if err = validateUndefinedPath(incoming, parts); err != nil {
			return WriteResult{}, err
		}
	}
	fail := func(e error) (WriteResult, error) {
		err := fmt.Errorf("Failed to read raw settings from %s: Error: %s", path, e)
		if s.options.Diagnostic != nil {
			s.options.Diagnostic(quoted("Error: " + err.Error()))
		}
		return WriteResult{Error: err}, nil
	}

	if err = s.options.FS.MkdirAll(filepath.Dir(path)); err != nil {
		return fail(err)
	}
	parsed, err := s.parseFile(path)
	if err != nil {
		return WriteResult{}, err
	}
	existing := parsed.Settings
	if existing == nil {
		content, e := readContent(s.options.FS, path)
		if e != nil && !os.IsNotExist(e) {
			var capability *CapabilityError
			if errors.As(e, &capability) {
				return WriteResult{}, e
			}
			return fail(e)
		}
		if e == nil {
			raw := parseJSONContent(content)
			if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				if s.options.Diagnostic != nil {
					if message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(strings.TrimPrefix(content, "\ufeff")); invalid {
						units, _ := protocol.DecodeJavaScriptJSONString(message)
						prefix, _ := protocol.DecodeJavaScriptJSONString(quoted("SyntaxError: "))
						s.options.Diagnostic(protocol.QuoteJavaScriptUTF16(append(prefix, units...)))
					}
				}
				return WriteResult{Error: fmt.Errorf("Invalid JSON syntax in settings file at %s", path)}, nil
			}
			if len(raw) > 0 && raw[0] != '{' {
				return WriteResult{}, &CapabilityError{"raw scalar/array merge target"}
			}
			var object protocol.JSONObject
			if err = object.UnmarshalJSON(raw); err != nil {
				return fail(err)
			}
			if err = s.checkCapabilities(object, true); err != nil {
				return WriteResult{}, err
			}
			existing = &object
		}
	}
	if existing == nil {
		o := protocol.NewJSONObject()
		existing = &o
	}
	if err = checkMergeValues(*existing); err != nil {
		return WriteResult{}, err
	}
	if err = mergeObjects(existing, incoming); err != nil {
		return WriteResult{}, err
	}
	for _, parts := range patch.UndefinedPaths {
		if err = setUndefinedPath(existing, parts); err != nil {
			return WriteResult{}, err
		}
	}
	raw, _ := existing.MarshalJSON()
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, protocol.StringifyValidatedJavaScriptJSON(raw), "", "  "); err != nil {
		return fail(err)
	}
	pretty.WriteByte('\n')
	s.emit(Effect{Kind: "mark", Path: path})
	s.options.State.MarkInternalWrite(path)
	if err = s.writePhysical(path, pretty.Bytes()); err != nil {
		return fail(err)
	}
	s.emit(Effect{Kind: "reset"})
	s.options.State.ResetSettingsCache()
	if source == "localSettings" && native != nil {
		root := s.options.OriginalCWD
		if projectOverride != "" {
			root = projectOverride
		}
		if err = native.StartLocalIgnore(root, ".claude/settings.local.json"); err != nil {
			return WriteResult{}, err
		}
	}
	return WriteResult{}, nil
}

// UsesState compares immutable service construction identity only.
func (s *Service) UsesState(state *State) bool {
	return s != nil && state != nil && s.options.State == state
}
