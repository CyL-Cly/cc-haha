package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
	"path/filepath"
	"unicode/utf16"
)

func (m *NativeMCPToolContext) prepareOrdinaryNestedWorkflow(ctx context.Context, ref json.RawMessage) (workflow.PreparedScript, error) {
	return ordinaryWorkflowOwner(m, ctx, func(scope *cooperative.Scope) (workflow.PreparedScript, error) {
		var script string
		if units, e := protocol.DecodeJavaScriptJSONString(ref); e == nil {
			name := string(utf16.Decode(units))
			found := false
			script, found, e = m.workflowDefinitions.NestedNamedScriptOnOwner(scope, name)
			if e != nil {
				return workflow.PreparedScript{}, e
			}
			if !found {
				return workflow.PreparedScript{}, fmt.Errorf("workflow(): unknown workflow '%s'", name)
			}
		} else {
			var object protocol.JSONObject
			if e := object.UnmarshalJSON(ref); e != nil || !object.IsObject() {
				return workflow.PreparedScript{}, errors.New("workflow() expects a saved workflow name or { scriptPath: '...' }")
			}
			path := object.String("scriptPath")
			physical := path
			if !filepath.IsAbs(physical) {
				physical = filepath.Join(m.state.Snapshot().CWD, physical)
			}
			future, e := nativeOwnedWork(m.command.Owner, m.binding, scope, func() (string, error) {
				raw, e := m.command.Native.readFile(physical)
				return string(protocol.DecodeJavaScriptUTF8(raw)), e
			})
			if e != nil {
				return workflow.PreparedScript{}, e
			}
			script, e = future.Await(scope)
			if e != nil {
				return workflow.PreparedScript{}, fmt.Errorf("workflow(): failed to read %s: %s", path, ordinaryWorkflowFileError(e, path))
			}
		}
		return m.workflowDefinitions.PrepareNestedOnOwner(scope, quoteMain(script))
	})
}
