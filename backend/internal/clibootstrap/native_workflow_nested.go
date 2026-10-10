package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func (m *MainPrint) prepareNativeNestedWorkflow(ctx context.Context, ref json.RawMessage) (workflow.PreparedScript, error) {
	var prepared workflow.PreparedScript
	if m.workflowDefinitions == nil {
		return prepared, cooperative.ErrRestricted
	}
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var script string
		if units, e := protocol.DecodeJavaScriptJSONString(ref); e == nil {
			name := string(utf16.Decode(units))
			found := false
			script, found, e = m.workflowDefinitions.NestedNamedScriptOnOwner(scope, name)
			if e != nil {
				return e
			}
			if !found {
				return fmt.Errorf("workflow(): unknown workflow '%s'", name)
			}
		} else {
			var object protocol.JSONObject
			if e := object.UnmarshalJSON(ref); e != nil || !object.IsObject() {
				return errors.New("workflow() expects a saved workflow name or { scriptPath: '...' }")
			}
			path := object.String("scriptPath")
			future, e := nativeSkillWork(m, scope, func() (string, error) {
				raw, e := m.options.Native.readFile(path)
				if e != nil {
					return "", e
				}
				return string(protocol.DecodeJavaScriptUTF8(raw)), nil
			})
			if e != nil {
				return e
			}
			script, e = future.Await(scope)
			if e != nil {
				return fmt.Errorf("workflow(): failed to read %s: %s", path, strings.TrimPrefix(storage.JavaScriptErrorText(e, storage.JavaScriptErrorContext{}), "Error: "))
			}
		}
		var e error
		prepared, e = m.workflowDefinitions.PrepareNestedOnOwner(scope, quoteMain(script))
		return e
	})
	if e == nil {
		e = receipt.Wait()
	}
	return prepared, e
}
