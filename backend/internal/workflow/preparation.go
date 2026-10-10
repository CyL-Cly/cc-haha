package workflow

import (
	"errors"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

type PreparedScript struct {
	Meta    protocol.JSONObject
	Program *goja.Program
}

func (s *Service) EnabledOnOwner(scope *cooperative.Scope) (bool, error) {
	if s == nil || !scope.BelongsTo(s.definitions.Owner) {
		return false, cooperative.ErrRestricted
	}
	reason, e := s.definitions.Gate.DisabledReason(scope)
	return reason == "", e
}
func (s *Service) PrepareOnOwner(scope *cooperative.Scope, script []byte) (PreparedScript, error) {
	var out PreparedScript
	if s == nil || !scope.BelongsTo(s.definitions.Owner) {
		return out, cooperative.ErrRestricted
	}
	reason, e := s.definitions.Gate.DisabledReason(scope)
	if e != nil {
		return out, e
	}
	if reason != "" {
		message := map[string]string{"env": "Dynamic workflows are disabled by CLAUDE_CODE_DISABLE_WORKFLOWS.", "managed": "Dynamic workflows are disabled by managed settings (`disableWorkflows`).", "settings": "Dynamic workflows are disabled in settings (`disableWorkflows`). Turn them back on in /config."}[reason]
		return out, errors.New(message)
	}
	units, e := protocol.DecodeJavaScriptJSONString(script)
	if e != nil {
		return out, e
	}
	parsed, e := s.definitions.Metadata.Parse(scope, units)
	if e != nil {
		return out, e
	}
	if parsed.ErrorUnits != nil {
		return out, errors.New(parsed.Error)
	}
	out.Program, e = CompileBody(parsed.ScriptBodyUnits)
	out.Meta = parsed.Meta.Clone()
	return out, e
}
func (s *Service) NamedScriptOnOwner(scope *cooperative.Scope, name string) (string, error) {
	if s == nil || !scope.BelongsTo(s.definitions.Owner) {
		return "", cooperative.ErrRestricted
	}
	future, e := s.definitions.BeginDefinitions(scope, nil)
	if e != nil {
		return "", e
	}
	definitions, e := future.Await(scope)
	if e != nil {
		return "", e
	}
	names := []string{}
	for _, definition := range definitions {
		names = append(names, definition.Object.String("name"))
		if definition.Object.String("name") == name {
			return definition.Object.String("script"), nil
		}
	}
	suffix := ""
	if len(names) > 0 {
		suffix = " Available: " + strings.Join(names, ", ")
	}
	return "", fmt.Errorf("Unknown workflow '%s'.%s", name, suffix)
}
