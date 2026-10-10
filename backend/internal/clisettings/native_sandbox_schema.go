package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Original sandboxTypes.ts declares a passthrough root, with ordinary stripping
// nested objects. Parsing these settings does not initialize or authorize a
// sandbox: the actual CLI lifecycle decides whether/when network state exists.
func parseNativeSandbox(raw json.RawMessage, file string) (json.RawMessage, []json.RawMessage) {
	optional := func(n *pluginschema.Node) *pluginschema.Node {
		return &pluginschema.Node{Type: "optional", InnerType: n}
	}
	field := func(name string, n *pluginschema.Node) protocol.JSONField {
		b, _ := json.Marshal(optional(n))
		return protocol.JSONField{Name: name, Value: b}
	}
	node := func(kind string) *pluginschema.Node { return &pluginschema.Node{Type: kind} }
	array := func() *pluginschema.Node { return &pluginschema.Node{Type: "array", Element: node("string")} }
	network := protocol.NewJSONObject(field("allowedDomains", array()), field("allowManagedDomainsOnly", node("boolean")), field("allowUnixSockets", array()), field("allowAllUnixSockets", node("boolean")), field("allowLocalBinding", node("boolean")), field("httpProxyPort", node("number")), field("socksProxyPort", node("number")))
	filesystem := protocol.NewJSONObject(field("allowWrite", array()), field("denyWrite", array()), field("denyRead", array()), field("allowRead", array()), field("allowManagedReadPathsOnly", node("boolean")))
	command, _ := json.Marshal(node("string"))
	ripgrep := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: command}, field("args", array()))
	shape := protocol.NewJSONObject(field("enabled", node("boolean")), field("failIfUnavailable", node("boolean")), field("autoAllowBashIfSandboxed", node("boolean")), field("allowUnsandboxedCommands", node("boolean")), field("network", &pluginschema.Node{Type: "object", Shape: network}), field("filesystem", &pluginschema.Node{Type: "object", Shape: filesystem}), field("ignoreViolations", &pluginschema.Node{Type: "record", KeyType: node("string"), ValueType: array()}), field("enableWeakerNestedSandbox", node("boolean")), field("enableWeakerNetworkIsolation", node("boolean")), field("excludedCommands", array()), field("ripgrep", &pluginschema.Node{Type: "object", Shape: ripgrep}))
	parsed, issues := pluginschema.Validate(optional(&pluginschema.Node{Type: "object", Shape: shape, Catchall: node("unknown")}), raw)
	errors := []json.RawMessage{}
	for _, issue := range issues {
		issue.Path = append([]any{"sandbox"}, issue.Path...)
		errors = append(errors, formatNativePluginIssue(file, issue))
	}
	return parsed, errors
}
