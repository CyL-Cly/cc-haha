package workflow

import (
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The original nested runner calls discovery and prepareWorkflowScript
// directly. It reuses the parent authorization; it does not run the launcher
// setting gate again after that parent's task has already been admitted.
func (s *Service) NestedNamedScriptOnOwner(scope *cooperative.Scope, name string) (string, bool, error) {
	if s == nil || !scope.BelongsTo(s.definitions.Owner) {
		return "", false, cooperative.ErrRestricted
	}
	future, e := s.definitions.BeginDefinitions(scope, nil)
	if e != nil {
		return "", false, e
	}
	definitions, e := future.Await(scope)
	if e != nil {
		return "", false, e
	}
	for _, definition := range definitions {
		if definition.Object.String("name") == name {
			return definition.Object.String("script"), true, nil
		}
	}
	return "", false, nil
}

func (s *Service) PrepareNestedOnOwner(scope *cooperative.Scope, script []byte) (PreparedScript, error) {
	var out PreparedScript
	if s == nil || !scope.BelongsTo(s.definitions.Owner) {
		return out, cooperative.ErrRestricted
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
		return out, errors.New("workflow(): " + parsed.Error)
	}
	out.Program, e = CompileBody(parsed.ScriptBodyUnits)
	out.Meta = parsed.Meta.Clone()
	if e != nil {
		return out, errors.New("workflow(): " + e.Error())
	}
	return out, nil
}
