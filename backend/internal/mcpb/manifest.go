package mcpb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

// This is the native strict manifest declaration and parser used by the
// compatible MCPB 1.2.0 SDK export (latest manifest version 0.3).
type manifestField struct {
	name string
	node *manifestNode
}
type manifestNode struct {
	kind                     string
	optional                 bool
	fields                   []manifestField
	element                  *manifestNode
	values                   []string
	format, pattern, message string
	minimum                  int
}
type manifestIssue struct {
	path    []string
	message string
}
type parsedManifest struct {
	value   json.RawMessage
	issues  []manifestIssue
	aborted bool
}

func scalar(kind string) *manifestNode          { return &manifestNode{kind: kind} }
func opt(node *manifestNode) *manifestNode      { copy := *node; copy.optional = true; return &copy }
func arrayOf(node *manifestNode) *manifestNode  { return &manifestNode{kind: "array", element: node} }
func recordOf(node *manifestNode) *manifestNode { return &manifestNode{kind: "record", element: node} }
func strict(fields ...manifestField) *manifestNode {
	return &manifestNode{kind: "object", fields: fields}
}
func enum(values ...string) *manifestNode                 { return &manifestNode{kind: "enum", values: values} }
func field(name string, node *manifestNode) manifestField { return manifestField{name, node} }
func str() *manifestNode                                  { return scalar("string") }
func urlString() *manifestNode                            { return &manifestNode{kind: "string", format: "url"} }

func manifestDeclaration() *manifestNode {
	serverConfig := func(partial bool) *manifestNode {
		command := str()
		if partial {
			command = opt(command)
		}
		return strict(field("command", command), field("args", opt(arrayOf(str()))), field("env", opt(recordOf(str()))))
	}
	config := serverConfig(false)
	config.fields = append(config.fields, field("platform_overrides", opt(recordOf(serverConfig(true)))))
	user := strict(field("type", enum("string", "number", "boolean", "directory", "file")), field("title", str()), field("description", str()), field("required", opt(scalar("boolean"))), field("default", opt(&manifestNode{kind: "config-value"})), field("multiple", opt(scalar("boolean"))), field("sensitive", opt(scalar("boolean"))), field("min", opt(scalar("number"))), field("max", opt(scalar("number"))))
	return strict(
		field("$schema", opt(str())), field("dxt_version", opt(&manifestNode{kind: "literal", values: []string{"0.3"}})), field("manifest_version", opt(&manifestNode{kind: "literal", values: []string{"0.3"}})), field("name", str()), field("display_name", opt(str())), field("version", str()), field("description", str()), field("long_description", opt(str())),
		field("author", strict(field("name", str()), field("email", opt(&manifestNode{kind: "string", format: "email"})), field("url", opt(urlString())))),
		field("repository", opt(strict(field("type", str()), field("url", urlString())))), field("homepage", opt(urlString())), field("documentation", opt(urlString())), field("support", opt(urlString())), field("icon", opt(str())),
		field("icons", opt(arrayOf(strict(field("src", str()), field("size", &manifestNode{kind: "string", pattern: `^\d+x\d+$`, message: `size must be in the format "WIDTHxHEIGHT" (e.g., "16x16")`}), field("theme", opt(&manifestNode{kind: "string", minimum: 1, message: "theme cannot be empty when provided"})))))),
		field("screenshots", opt(arrayOf(str()))), field("localization", opt(strict(field("resources", &manifestNode{kind: "string", pattern: `(?i)\$\{locale\}`, message: `resources must include a "${locale}" placeholder`}), field("default_locale", &manifestNode{kind: "string", pattern: `^[A-Za-z0-9]{2,8}(?:-[A-Za-z0-9]{1,8})*$`, message: "default_locale must be a valid BCP 47 locale identifier"})))),
		field("server", strict(field("type", enum("python", "node", "binary")), field("entry_point", str()), field("mcp_config", config))),
		field("tools", opt(arrayOf(strict(field("name", str()), field("description", opt(str())))))), field("tools_generated", opt(scalar("boolean"))),
		field("prompts", opt(arrayOf(strict(field("name", str()), field("description", opt(str())), field("arguments", opt(arrayOf(str()))), field("text", str()))))), field("prompts_generated", opt(scalar("boolean"))),
		field("keywords", opt(arrayOf(str()))), field("license", opt(str())), field("privacy_policies", opt(arrayOf(urlString()))),
		field("compatibility", opt(strict(field("claude_desktop", opt(str())), field("platforms", opt(arrayOf(enum("darwin", "win32", "linux")))), field("runtimes", opt(strict(field("python", opt(str())), field("node", opt(str())))))))),
		field("user_config", opt(recordOf(user))), field("_meta", opt(recordOf(recordOf(scalar("any"))))),
	)
}

var manifestSpec = manifestDeclaration()
var manifestEmail = regexp.MustCompile(`(?i)^[A-Z0-9_'+.\-]*[A-Z0-9_+\-]@([A-Z0-9][A-Z0-9\-]*\.)+[A-Z]{2,}$`)

// JSONMessage preserves the original error's lone UTF-16 units at JSON API
// boundaries; Go's ordinary error text remains readable for native logging.
type ManifestError struct{ message json.RawMessage }

func (e *ManifestError) Error() string {
	var value string
	_ = json.Unmarshal(e.message, &value)
	return value
}
func (e *ManifestError) JSONMessage() json.RawMessage {
	return append(json.RawMessage(nil), e.message...)
}

func valueKind(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 'n':
		return "null"
	case 't', 'f':
		return "boolean"
	}
	return "number"
}
func quote(value string) json.RawMessage { data, _ := json.Marshal(value); return data }
func encode(value any) json.RawMessage   { data, _ := json.Marshal(value); return data }
func appendPath(path []string, value string) []string {
	return append(append([]string{}, path...), value)
}
func invalidType(path []string, expected string, raw json.RawMessage) parsedManifest {
	message := fmt.Sprintf("Expected %s, received %s", expected, valueKind(raw))
	if raw == nil {
		message = "Required"
	}
	return parsedManifest{issues: []manifestIssue{{path, message}}, aborted: true}
}
func rawProperty(value protocol.JSONObject, name json.RawMessage) json.RawMessage {
	for _, property := range value.OwnProperties() {
		if bytes.Equal(property.NameJSON, name) {
			return property.Value
		}
	}
	return nil
}

func parseManifestNode(node *manifestNode, raw json.RawMessage, path []string) parsedManifest {
	if raw == nil && node.optional {
		return parsedManifest{}
	}
	kind := valueKind(raw)
	if node.kind == "any" {
		return parsedManifest{value: raw}
	}
	if node.kind == "literal" {
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw), quote(node.values[0])) {
			return parsedManifest{issues: []manifestIssue{{path, "Invalid literal value, expected " + string(quote(node.values[0]))}}, aborted: true}
		}
		return parsedManifest{value: raw}
	}
	if node.kind == "enum" {
		expected := "'" + strings.Join(node.values, "' | '") + "'"
		if kind != "string" {
			return invalidType(path, expected, raw)
		}
		for _, value := range node.values {
			if bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw), quote(value)) {
				return parsedManifest{value: raw}
			}
		}
		return parsedManifest{issues: []manifestIssue{{path, "Invalid enum value. Expected " + expected + ", received '" + text(raw) + "'"}}, aborted: true}
	}
	if node.kind == "config-value" {
		if kind == "string" || kind == "number" || kind == "boolean" {
			return parsedManifest{value: raw}
		}
		if kind == "array" {
			result := parseManifestNode(arrayOf(str()), raw, path)
			if len(result.issues) == 0 {
				return result
			}
		}
		return parsedManifest{issues: []manifestIssue{{path, "Invalid input"}}, aborted: true}
	}
	expected := node.kind
	if expected == "record" {
		expected = "object"
	}
	if expected != kind {
		return invalidType(path, expected, raw)
	}
	result := parsedManifest{value: raw}
	if kind == "string" {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		value := string(utf16.Decode(units))
		bad, message := false, node.message
		switch node.format {
		case "email":
			bad = strings.HasPrefix(value, ".") || strings.Contains(value, "..") || !manifestEmail.MatchString(value)
			message = "Invalid email"
		case "url":
			_, err := whatwg.Parse(value)
			bad = err != nil
			message = "Invalid url"
		}
		if node.pattern != "" {
			bad = !regexp.MustCompile(node.pattern).MatchString(value)
		}
		if node.minimum > 0 && len(units) < node.minimum {
			bad = true
		}
		if bad {
			result.issues = append(result.issues, manifestIssue{path, message})
		}
		return result
	}
	if kind == "array" {
		var items []json.RawMessage
		_ = json.Unmarshal(raw, &items)
		parsed := []json.RawMessage{}
		for i, item := range items {
			child := parseManifestNode(node.element, item, appendPath(path, fmt.Sprint(i)))
			result.issues = append(result.issues, child.issues...)
			result.aborted = result.aborted || child.aborted
			parsed = append(parsed, child.value)
		}
		result.value = encode(parsed)
		return result
	}
	if kind == "object" {
		value := object(raw)
		parsed := protocol.NewJSONObject()
		known := map[string]bool{}
		if node.kind == "record" {
			for _, property := range value.OwnProperties() {
				var key string
				_ = json.Unmarshal(property.NameJSON, &key)
				child := parseManifestNode(node.element, property.Value, appendPath(path, key))
				result.issues = append(result.issues, child.issues...)
				result.aborted = result.aborted || child.aborted
				if key != "__proto__" {
					_ = parsed.SetPropertyJSON(property.NameJSON, child.value)
				}
			}
		} else {
			for _, field := range node.fields {
				known[field.name] = true
				child := parseManifestNode(field.node, value.Get(field.name), appendPath(path, field.name))
				result.issues = append(result.issues, child.issues...)
				result.aborted = result.aborted || child.aborted
				if child.value != nil {
					parsed.Set(field.name, child.value)
				}
			}
			extra := []string{}
			for _, property := range value.OwnProperties() {
				var key string
				_ = json.Unmarshal(property.NameJSON, &key)
				if !known[key] {
					extra = append(extra, key)
				}
			}
			if len(extra) > 0 {
				result.issues = append(result.issues, manifestIssue{path, "Unrecognized key(s) in object: '" + strings.Join(extra, "', '") + "'"})
			}
		}
		result.value = encode(parsed)
	}
	return result
}

func ParseManifest(data []byte) (protocol.JSONObject, error) {
	raw := bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{0xef, 0xbb, 0xbf})
	if !json.Valid(raw) {
		message, _ := provider.PinnedJavaScriptJSONSyntaxMessage(string(raw))
		units, _ := protocol.DecodeJavaScriptJSONString(message)
		return protocol.JSONObject{}, &ManifestError{protocol.QuoteJavaScriptUTF16(append(utf16.Encode([]rune("Invalid JSON in manifest.json: ")), units...))}
	}
	parsed := parseManifestNode(manifestSpec, raw, nil)
	value := object(parsed.value)
	if !parsed.aborted && !truthy(value.Get("dxt_version")) && !truthy(value.Get("manifest_version")) {
		parsed.issues = append(parsed.issues, manifestIssue{nil, "Either 'dxt_version' (deprecated) or 'manifest_version' must be provided"})
	}
	if len(parsed.issues) != 0 {
		fields := protocol.NewJSONObject()
		forms := []string{}
		for _, issue := range parsed.issues {
			if len(issue.path) == 0 {
				forms = append(forms, issue.message)
				continue
			}
			key := issue.path[0]
			var list []string
			_ = json.Unmarshal(fields.Get(key), &list)
			list = append(list, issue.message)
			fields.Set(key, encode(list))
		}
		messages := []string{}
		for _, field := range fields.OwnProperties() {
			var key string
			_ = json.Unmarshal(field.NameJSON, &key)
			var list []string
			_ = json.Unmarshal(field.Value, &list)
			messages = append(messages, key+": "+strings.Join(list, ", "))
		}
		messages = append(messages, forms...)
		return protocol.JSONObject{}, fmt.Errorf("Invalid manifest: %s", strings.Join(messages, "; "))
	}
	return value, nil
}
