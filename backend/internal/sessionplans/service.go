// Package sessionplans implements Source plans.ts on the existing Bootstrap
// session/slug authority. It owns no permission policy, Agent or transcript.
package sessionplans

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Files interface {
	Stat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, fs.FileMode) error
	MkdirAll(string, fs.FileMode) error
}
type NativeFiles struct{}

func (NativeFiles) Stat(p string) (fs.FileInfo, error) { return os.Stat(p) }
func (NativeFiles) ReadFile(p string) ([]byte, error)  { return os.ReadFile(p) }
func (NativeFiles) WriteFile(p string, data []byte, mode fs.FileMode) error {
	return os.WriteFile(p, data, mode)
}
func (NativeFiles) MkdirAll(p string, mode fs.FileMode) error { return os.MkdirAll(p, mode) }

type Options struct {
	Bootstrap       *bootstrapstate.State
	ConfigDir, CWD  string
	InitialSettings func() (*protocol.JSONObject, error)
	Files           Files
	WordSlug        func() (string, error) // The crypto random primitive in fixtures.
	OnError         func(error)
}
type Service struct {
	options   Options
	directory string
}
type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "session plans capability is not migrated: " + e.Boundary
}
func New(o Options) (*Service, error) {
	if o.Bootstrap == nil || !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.CWD) || o.InitialSettings == nil {
		return nil, errors.New("plans require actual Bootstrap, settings and resolved directories")
	}
	if o.Files == nil {
		o.Files = NativeFiles{}
	}
	if o.WordSlug == nil {
		o.WordSlug = GenerateWordSlug
	}
	return &Service{options: o}, nil
}
func (s *Service) check(scope *cooperative.Scope) error {
	if s == nil {
		return cooperative.ErrRestricted
	}
	return s.options.Bootstrap.CheckScope(scope)
}
func (s *Service) fail(e error) {
	if e != nil && s.options.OnError != nil {
		s.options.OnError(e)
	}
}
func (s *Service) Directory(scope *cooperative.Scope) (string, error) {
	if e := s.check(scope); e != nil {
		return "", e
	}
	if s.directory != "" {
		return s.directory, nil
	}
	settings, e := s.options.InitialSettings()
	if e != nil {
		return "", e
	}
	if settings == nil {
		return "", errors.New("plans settings are unavailable")
	}
	path := filepath.Join(s.options.ConfigDir, "plans")
	raw := settings.Get("plansDirectory")
	if truthy(raw) {
		var configured string
		if json.Unmarshal(raw, &configured) != nil {
			return "", &CapabilityError{"plansDirectory must be the original string"}
		}
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		for i := 0; i < len(units); i++ {
			if units[i] >= 0xd800 && units[i] <= 0xdbff {
				if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return "", &CapabilityError{"plansDirectory UTF16 filesystem path"}
				}
				i++
			} else if units[i] >= 0xdc00 && units[i] <= 0xdfff {
				return "", &CapabilityError{"plansDirectory UTF16 filesystem path"}
			}
		}
		resolved := configured
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(s.options.CWD, configured)
		}
		resolved = filepath.Clean(resolved)
		if resolved == s.options.CWD || strings.HasPrefix(resolved, s.options.CWD+string(filepath.Separator)) {
			path = resolved
		} else {
			s.fail(errors.New("plansDirectory must be within project root: " + configured))
		}
	}
	s.fail(s.options.Files.MkdirAll(path, 0777))
	s.directory = path
	return path, nil
}
func (s *Service) Slug(scope *cooperative.Scope) (string, error) {
	if e := s.check(scope); e != nil {
		return "", e
	}
	return s.SlugForSession(scope, s.options.Bootstrap.SessionID())
}

// SlugForSession is getPlanSlug(targetSessionId)'s explicit-id overload. A
// fork calls it before switchSession, so the old process SID must stay intact.
func (s *Service) SlugForSession(scope *cooperative.Scope, id string) (string, error) {
	if e := s.check(scope); e != nil {
		return "", e
	}
	cache, e := s.options.Bootstrap.PlanSlugCache(scope)
	if e != nil {
		return "", e
	}
	slug, exists, e := cache.Get(scope, id)
	if e != nil || exists && slug != "" {
		return slug, e
	}
	directory, e := s.Directory(scope)
	if e != nil {
		return "", e
	}
	for i := 0; i < 10; i++ {
		slug, e = s.options.WordSlug()
		if e != nil {
			return "", e
		}
		if _, e = s.options.Files.Stat(filepath.Join(directory, slug+".md")); e != nil {
			break
		}
	}
	if e := cache.Set(scope, id, slug); e != nil {
		return "", e
	}
	return slug, nil
}
func (s *Service) Path(scope *cooperative.Scope, agentID string) (string, error) {
	slug, e := s.Slug(scope)
	if e != nil {
		return "", e
	}
	directory, e := s.Directory(scope)
	if e != nil {
		return "", e
	}
	name := slug + ".md"
	if agentID != "" {
		name = slug + "-agent-" + agentID + ".md"
	}
	return filepath.Join(directory, name), nil
}
func (s *Service) Read(scope *cooperative.Scope, agentID string) (json.RawMessage, string, error) {
	path, e := s.Path(scope, agentID)
	if e != nil {
		return nil, "", e
	}
	data, e := s.options.Files.ReadFile(path)
	if e != nil {
		if !errors.Is(e, fs.ErrNotExist) {
			s.fail(e)
		}
		return json.RawMessage("null"), path, nil
	}
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(string(protocol.DecodeJavaScriptUTF8(data))))), path, nil
}
func (s *Service) NormalizeExitInput(scope *cooperative.Scope, in protocol.JSONObject, agentID string) (protocol.JSONObject, error) {
	plan, path, e := s.Read(scope, agentID)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	result := in.Clone()
	if string(plan) != "null" {
		result.Set("plan", plan)
		result.Set("planFilePath", protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(path))))
	}
	return result, nil
}
func (s *Service) WriteEdited(scope *cooperative.Scope, agentID string, plan json.RawMessage) error {
	write, e := s.PrepareEditedWrite(scope, agentID, plan)
	if e != nil {
		return e
	}
	return s.LogEditedWriteError(scope, write())
}

// PrepareEditedWrite freezes paths/bytes on the session owner. The returned
// native primitive contains no Scope or permission state and may run off-owner,
// matching Source's awaited fs/promises.writeFile before the mode transition.
func (s *Service) PrepareEditedWrite(scope *cooperative.Scope, agentID string, plan json.RawMessage) (func() error, error) {
	path, e := s.Path(scope, agentID)
	if e != nil {
		return nil, e
	}
	units, e := protocol.DecodeJavaScriptJSONString(plan)
	if e != nil {
		return nil, e
	}
	// Source writeFile(...,'utf-8') replaces lone UTF16 surrogates on disk.
	data := []byte(string(utf16.Decode(units)))
	files := s.options.Files
	return func() error { return files.WriteFile(path, data, 0666) }, nil
}

func (s *Service) LogEditedWriteError(scope *cooperative.Scope, failure error) error {
	if e := s.check(scope); e != nil {
		return e
	}
	// Source catches a physical write failure, reports it, and still exits plan
	// mode with the user's edited text. Logging remains on the same owner.
	s.fail(failure)
	return nil
}

func truthy(raw json.RawMessage) bool {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}
func (s *Service) IsSessionFile(scope *cooperative.Scope, path string) (bool, error) {
	slug, e := s.Slug(scope)
	if e != nil {
		return false, e
	}
	directory, e := s.Directory(scope)
	if e != nil {
		return false, e
	}
	normalized := filepath.Clean(path)
	// Keep Source's lexical prefix check, including other suffixes. The
	// normalized path must begin with this session's actual cached slug.
	return strings.HasPrefix(normalized, filepath.Join(directory, slug)) && strings.HasSuffix(normalized, ".md"), nil
}

// Working-directory changes clear only plans.ts's directory memo. The per-SID
// random plan slug remains the same original Bootstrap entry.
func (s *Service) SetWorkingDirectory(scope *cooperative.Scope, cwd string) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if !filepath.IsAbs(cwd) {
		return &CapabilityError{"actual working directory"}
	}
	s.options.CWD = cwd
	s.directory = ""
	return nil
}
