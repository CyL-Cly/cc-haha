package agentcatalog

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"path/filepath"
	"time"
)

type Files interface {
	Stat(string) (fs.FileInfo, error)
	Lstat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	ReadDir(string) ([]fs.DirEntry, error)
	Realpath(string) (string, error)
	Identity(fs.FileInfo) string
}
type Options struct {
	Owner                                           *cooperative.Owner
	Files                                           Files
	Home, CWD, ProjectRoot, ConfigHome, ManagedRoot string
	NativeConfigHome                                func() string
	EnabledSources                                  []string
	Environment                                     func(string) string
	Settings                                        func(string) (*protocol.JSONObject, error)
	Now                                             func() time.Time
	NonInteractive                                  bool
	// Runtime memory and parser tool expansion share the actual session gate.
	AutoMemoryEnabled func(*cooperative.Scope) (bool, error)
	MemoryPrompt      func(*cooperative.Scope, string, string) (string, error)
	FindMarkdown      func(string) ([]string, error)
	// Only an actual plugin producer may return definitions. nil retains the
	// explicit missing-loader capability, never an injected empty list.
	Plugins func(*cooperative.Scope) ([]Definition, error)
}
type Failure struct{ Path, Error string }
type Result struct {
	All, Active []Definition
	FailedFiles []Failure
}
type Service struct {
	options Options
	cache   *Result
	cli     []Definition
	sdk     []Definition
}

func New(o Options) (*Service, error) {
	if o.Owner == nil || o.Files == nil || o.Environment == nil || o.Settings == nil || o.Now == nil {
		return nil, cap("resolved owner/files/settings/environment/clock")
	}
	for _, p := range []string{o.Home, o.CWD, o.ProjectRoot, o.ConfigHome, o.ManagedRoot} {
		if !filepath.IsAbs(p) {
			return nil, cap("absolute discovery paths")
		}
	}
	for _, source := range o.EnabledSources {
		switch source {
		case "userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings":
		default:
			return nil, cap("unknown settings source " + source)
		}
	}
	return &Service{options: o}, nil
}
func (s *Service) check(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}
func (s *Service) enabled(source string) bool {
	for _, v := range s.options.EnabledSources {
		if v == source {
			return true
		}
	}
	return false
}
func (s *Service) locked() (bool, error) {
	p, e := s.options.Settings("policySettings")
	if e != nil {
		return false, e
	}
	if p == nil {
		return false, nil
	}
	raw := p.Get("strictPluginOnlyCustomization")
	if string(raw) == "true" {
		return true, nil
	}
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		for _, s := range values {
			if s == "agents" {
				return true, nil
			}
		}
	}
	return false, nil
}
func (s *Service) List(scope *cooperative.Scope) (*Result, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if s.cache != nil {
		return s.cache, nil
	}
	if boolEnv(s.options.Environment("CLAUDE_CODE_SIMPLE")) {
		built, e := s.builtin(scope)
		if e != nil {
			return nil, e
		}
		all := append(built, s.cli...)
		s.cache = s.resultWithSDK(all, nil)
		return s.cache, nil
	}
	custom, failed, e := s.markdown()
	if e != nil {
		var c *CapabilityError
		if errors.As(e, &c) {
			return nil, e
		}
		// Source catches discovery failure at this outer boundary, returning builtins
		// and failedFiles. Unsupported Go capabilities must never masquerade as this.
		built, builtError := s.builtin(scope)
		if builtError != nil {
			return nil, builtError
		}
		s.cache = s.resultWithSDK(built, []Failure{{"unknown", e.Error()}})
		return s.cache, nil
	}
	memory, e := s.autoMemory(scope)
	if e != nil {
		return nil, e
	}
	for i := range custom {
		if e = enrichMarkdownDefinition(&custom[i], memory); e != nil {
			return nil, e
		}
	}
	if s.options.Plugins == nil {
		return nil, cap("actual plugin cache loader")
	}
	plugins, e := s.options.Plugins(scope)
	if e != nil {
		var c *CapabilityError
		if errors.As(e, &c) {
			return nil, e
		}
		built, builtError := s.builtin(scope)
		if builtError != nil {
			return nil, builtError
		}
		s.cache = s.resultWithSDK(built, []Failure{{"unknown", e.Error()}})
		return s.cache, nil
	}
	built, e := s.builtin(scope)
	if e != nil {
		return nil, e
	}
	all := append(append(append(append([]Definition{}, built...), plugins...), custom...), s.cli...)
	s.cache = s.resultWithSDK(all, failed)
	return s.cache, nil
}
func (s *Service) Clear(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	s.cache = nil
	return nil
}
