package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string { return "Workflow capability is not migrated: " + e.Boundary }

type APIError struct {
	Status        int
	Message, Code string
}

func (e *APIError) Error() string     { return e.Message }
func badRequest(message string) error { return &APIError{400, message, "BAD_REQUEST"} }

type DefinitionFiles struct {
	Lstat     func(string) (fs.FileInfo, error)
	MkdirAll  func(string) error
	WriteFile func(string, []byte) error
	Remove    func(string) error
}

func NativeDefinitionFiles() DefinitionFiles {
	return DefinitionFiles{Lstat: os.Lstat, MkdirAll: func(path string) error { return os.MkdirAll(path, 0777) }, WriteFile: func(path string, value []byte) error { return os.WriteFile(path, value, 0666) }, Remove: os.Remove}
}

// DefinitionOptions bind the server and shared CLI to the existing owner,
// metadata parser, settings gate and discovery source. No Harness is created.
type DefinitionOptions struct {
	Owner                              *cooperative.Owner
	Metadata                           *workflowcommands.Metadata
	Gate                               *workflowcommands.Gate
	BeginDefinitions                   func(*cooperative.Scope, *string) (*ownerasync.OwnerFuture[[]workflowcommands.Definition], error)
	BeginCommands                      func(*cooperative.Scope, string) (*ownerasync.OwnerFuture[[]*workflowcommands.Command], error)
	ClearCommandCache                  func(*cooperative.Scope) error
	ProjectDirectory                   func(*cooperative.Scope, string) (string, error)
	Compile                            func(*cooperative.Scope, []uint16) error
	ConfigDir, OriginalCWD, ProcessCWD string
	Files                              DefinitionFiles
	History                            *History
}
type Service struct {
	definitions DefinitionOptions
	history     *History
}

func New(options DefinitionOptions) (*Service, error) {
	if options.Owner == nil || options.Metadata == nil || options.Gate == nil || options.BeginDefinitions == nil || options.ProjectDirectory == nil || !filepath.IsAbs(options.ConfigDir) || !filepath.IsAbs(options.OriginalCWD) || !filepath.IsAbs(options.ProcessCWD) || options.Files.Lstat == nil || options.Files.MkdirAll == nil || options.Files.WriteFile == nil || options.Files.Remove == nil {
		return nil, errors.New("Workflow definitions require the actual owner/metadata/settings/discovery/native files")
	}
	return &Service{definitions: options, history: options.History}, nil
}
func (s *Service) enabled(scope *cooperative.Scope) error {
	reason, e := s.definitions.Gate.DisabledReason(scope)
	if e != nil {
		return e
	}
	if reason != "" {
		return &APIError{403, "Dynamic workflows are disabled (`disableWorkflows`).", "WORKFLOWS_DISABLED"}
	}
	return nil
}
func (s *Service) run(ctx context.Context, fn func(*cooperative.Scope) error) error {
	receipt, e := s.definitions.Owner.Submit(ctx, cooperative.Input, fn)
	if e != nil {
		return e
	}
	return receipt.Wait()
}
func (s *Service) ListDefinitions(ctx context.Context, cwd *string) ([]protocol.JSONObject, error) {
	result := []protocol.JSONObject{}
	e := s.run(ctx, func(scope *cooperative.Scope) error {
		if e := s.enabled(scope); e != nil {
			return e
		}
		future, e := s.definitions.BeginDefinitions(scope, cwd)
		if e != nil {
			return e
		}
		definitions, e := future.Await(scope)
		if e != nil {
			return e
		}
		for _, definition := range definitions {
			result = append(result, definitionSummary(definition.Object, false))
		}
		return nil
	})
	return result, e
}
func definitionSummary(definition protocol.JSONObject, includeScript bool) protocol.JSONObject {
	result := protocol.NewJSONObject()
	for _, key := range []string{"name", "description", "whenToUse", "source", "phases", "filePath"} {
		result.Set(key, definition.Get(key))
	}
	if includeScript {
		result.Set("script", definition.Get("script"))
	}
	return result
}
func (s *Service) GetDefinition(ctx context.Context, name string, cwd *string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	e := s.run(ctx, func(scope *cooperative.Scope) error {
		if e := s.enabled(scope); e != nil {
			return e
		}
		future, e := s.definitions.BeginDefinitions(scope, cwd)
		if e != nil {
			return e
		}
		definitions, e := future.Await(scope)
		if e != nil {
			return e
		}
		for _, definition := range definitions {
			if definition.Object.String("name") == name {
				result = definitionSummary(definition.Object, true)
				return nil
			}
		}
		return &APIError{404, "Unknown workflow: " + name, "NOT_FOUND"}
	})
	return result, e
}
func quoted(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func (s *Service) Validate(ctx context.Context, script json.RawMessage) (protocol.JSONObject, error) {
	result := protocol.NewJSONObject()
	e := s.run(ctx, func(scope *cooperative.Scope) error {
		if e := s.enabled(scope); e != nil {
			return e
		}
		units, e := protocol.DecodeJavaScriptJSONString(script)
		if e != nil {
			return badRequest("`script` is required")
		}
		parsed, e := s.definitions.Metadata.Parse(scope, units)
		if e != nil {
			return e
		}
		if parsed.ErrorUnits != nil {
			result.Set("ok", []byte("false"))
			result.Set("error", protocol.QuoteJavaScriptUTF16(parsed.ErrorUnits))
			return nil
		}
		if s.definitions.Compile == nil {
			return &CapabilityError{"original script compiler"}
		}
		if e := s.definitions.Compile(scope, parsed.ScriptBodyUnits); e != nil {
			var cap *CapabilityError
			if errors.As(e, &cap) {
				return e
			}
			result.Set("ok", []byte("false"))
			result.Set("error", quoted(e.Error()))
			return nil
		}
		banned, e := s.definitions.Metadata.UsesBanned(scope, parsed.ScriptBodyUnits)
		if e != nil {
			return e
		}
		result.Set("ok", []byte("true"))
		if banned {
			result.Set("warnings", []byte(`["Date.now(), new Date() and Math.random() throw at run time — they would make a resume replay diverge."]`))
		}
		for _, key := range []string{"name", "description", "phases"} {
			result.Set(key, parsed.Meta.Get(key))
		}
		return nil
	})
	return result, e
}

var validDefinitionName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
var desktopReservedNames = map[string]bool{"mcp": true, "skills": true, "save-workflow": true, "help": true, "status": true, "cost": true, "context": true, "config": true, "plugin": true, "plugins": true, "settings": true, "memory": true, "doctor": true, "model": true}

func (s *Service) SaveDefinition(ctx context.Context, script json.RawMessage, scopeName string, cwd *string, name json.RawMessage) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	e := s.run(ctx, func(scope *cooperative.Scope) error {
		if e := s.enabled(scope); e != nil {
			return e
		}
		units, e := protocol.DecodeJavaScriptJSONString(script)
		if e != nil {
			return badRequest("`script` is required")
		}
		if name != nil {
			requested, e := protocol.DecodeJavaScriptJSONString(name)
			if e != nil {
				return badRequest("`name` must be a string")
			}
			text := string(utf16.Decode(requested))
			if desktopReservedNames[strings.ToLower(javascriptTrim(text))] {
				return badRequest("Workflow name is reserved by the desktop: " + text)
			}
			renamed, parsed, e := s.definitions.Metadata.Rename(scope, units, requested)
			if e != nil {
				return e
			}
			if parsed.ErrorUnits != nil {
				return badRequest(string(utf16.Decode(parsed.ErrorUnits)))
			}
			units = renamed
		}
		parsed, e := s.definitions.Metadata.Parse(scope, units)
		if e != nil {
			return e
		}
		if parsed.ErrorUnits != nil {
			return badRequest(string(utf16.Decode(parsed.ErrorUnits)))
		}
		if s.definitions.Compile == nil {
			return &CapabilityError{"original script compiler"}
		}
		if e := s.definitions.Compile(scope, parsed.ScriptBodyUnits); e != nil {
			var cap *CapabilityError
			if errors.As(e, &cap) {
				return e
			}
			return badRequest(e.Error())
		}
		directory := filepath.Join(s.definitions.ConfigDir, "workflows")
		originalCWD := s.definitions.OriginalCWD
		if cwd != nil {
			originalCWD = *cwd
		}
		if scopeName == "project" {
			directory, e = s.definitions.ProjectDirectory(scope, originalCWD)
			if e != nil {
				return e
			}
			for _, path := range []string{filepath.Join(directory, "..", "..", ".claude"), directory, filepath.Join(directory, parsed.Meta.String("name")+".js")} {
				if e := s.assertSaveNotSymlink(path); e != nil {
					return e
				}
			}
		} else if e := s.assertSaveNotSymlink(filepath.Join(directory, parsed.Meta.String("name")+".js")); e != nil {
			return e
		}
		file := filepath.Join(directory, parsed.Meta.String("name")+".js")
		data := []byte(string(utf16.Decode(units)))
		if e := s.definitions.Files.MkdirAll(directory); e != nil {
			return badRequest(e.Error())
		}
		if e := s.definitions.Files.WriteFile(file, data); e != nil {
			return badRequest(e.Error())
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: parsed.Meta.Get("name")}, protocol.JSONField{Name: "filePath", Value: quoted(file)})
		return nil
	})
	return result, e
}
func (s *Service) DeleteDefinition(ctx context.Context, name, scopeName string, cwd *string) error {
	return s.run(ctx, func(scope *cooperative.Scope) error {
		if e := s.enabled(scope); e != nil {
			return e
		}
		if !validDefinitionName.MatchString(name) {
			return badRequest("Invalid workflow name: " + name)
		}
		directory := filepath.Join(s.definitions.ConfigDir, "workflows")
		if scopeName == "project" {
			current := s.definitions.ProcessCWD
			if cwd != nil {
				current = *cwd
			}
			var e error
			directory, e = s.definitions.ProjectDirectory(scope, current)
			if e != nil {
				return e
			}
		}
		file := filepath.Join(directory, name+".js")
		if e := s.assertNotSymlink(file); e != nil {
			return e
		}
		if e := s.definitions.Files.Remove(file); e != nil {
			if os.IsNotExist(e) {
				return &APIError{404, "No saved workflow at " + file, "NOT_FOUND"}
			}
			return e
		}
		return nil
	})
}
func (s *Service) assertNotSymlink(path string) error {
	info, e := s.definitions.Files.Lstat(path)
	if e != nil {
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return badRequest(fmt.Sprintf("Refusing to write through a symlink: %s", path))
	}
	return nil
}
func (s *Service) assertSaveNotSymlink(path string) error {
	info, e := s.definitions.Files.Lstat(path)
	if e != nil {
		return nil
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return badRequest("Refusing to write through a symlink: " + path)
	}
	return nil
}
