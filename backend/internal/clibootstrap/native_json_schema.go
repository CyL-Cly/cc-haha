package clibootstrap

import (
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

// Source parses this CLI option only for noninteractive execution. The flag
// creates a transient final-output tool after the user's normal tool filter.
func (m *MainPrint) initializeNativeJSONSchema(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.options.Interactive || m.agentChild || m.options.AgentID != "" || m.options.Arguments.JSONSchema == "" {
		return nil
	}
	if !m.options.Native.NativeProcess {
		return mainCap("public CLI JSON schema actual native process")
	}
	schema, e := protocol.ParseAndStringifyJavaScriptJSON([]byte(m.options.Arguments.JSONSchema))
	if e != nil {
		return fmt.Errorf("JSON schema parse: %w", e)
	}
	resources, e := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if e != nil {
		return e
	}
	binding, e := sdkhost.NewNativeJSONSchema(m.Owner, resources, schema)
	if e != nil {
		return e
	}
	if binding == nil {
		return nil
	}
	m.nativeJSONSchema = binding
	if binding.Synthetic() != nil {
		if m.nativeSynthetic != nil {
			return cooperative.ErrRestricted
		}
		m.nativeSynthetic = binding.Synthetic()
	}
	if report := m.nativeFileReporter(); report != nil {
		event := analytics.Event{Name: "tengu_structured_output_failure", Metadata: protocol.NewJSONObject(protocol.JSONField{Name: "error", Value: quoteMain("Invalid JSON schema")})}
		if binding.Synthetic() != nil {
			var value protocol.JSONObject
			_ = value.UnmarshalJSON(schema)
			props, _ := value.Object("properties")
			required := value.Get("required")
			event = analytics.Event{Name: "tengu_structured_output_enabled", Metadata: protocol.NewJSONObject(protocol.JSONField{Name: "schema_property_count", Value: rawMain(len(props.OwnProperties()))}, protocol.JSONField{Name: "has_required_fields", Value: rawMain(len(required) > 0 && string(required) != "false" && string(required) != "null" && string(required) != "0" && string(required) != `""`)})}
		}
		return report(scope, event)
	}
	return nil
}
