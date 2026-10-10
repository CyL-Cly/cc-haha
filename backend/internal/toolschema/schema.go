package toolschema

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func quote(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func raw(object protocol.JSONObject) json.RawMessage { value, _ := object.MarshalJSON(); return value }
func array(value any) json.RawMessage                { result, _ := json.Marshal(value); return result }
func parseObject(value json.RawMessage) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	err := json.Unmarshal(value, &result)
	return result, err
}
func stringify(value json.RawMessage) (json.RawMessage, error) {
	return protocol.ParseAndStringifyJavaScriptJSON(value)
}

func ParseDefinition(record protocol.JSONObject) (Definition, error) {
	var definition Definition
	if err := json.Unmarshal(raw(record), &definition); err != nil {
		return definition, unsupported("malformed original definition: " + err.Error())
	}
	if definition.Name == "" {
		return definition, unsupported("missing original tool name")
	}
	if err := scalarName(record.Get("name")); err != nil {
		return definition, err
	}
	var aliasSources []json.RawMessage
	if value := record.Get("aliases"); value != nil {
		if err := json.Unmarshal(value, &aliasSources); err != nil {
			return definition, unsupported("original aliases must be strings")
		}
		for _, alias := range aliasSources {
			if err := scalarName(alias); err != nil {
				return definition, err
			}
		}
	}
	allowed := map[string]bool{"kind": true, "name": true, "inputSchema": true, "inputJSONSchema": true, "prompt": true, "description": true, "strict": true, "aliases": true, "concurrencySafe": true, "readOnly": true}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw(record), &fields)
	for key := range fields {
		if !allowed[key] {
			return definition, unsupported("original definition field " + key)
		}
	}
	if definition.Kind == "builtin" {
		if len(fields) != 2 {
			return definition, unsupported("builtin definition override")
		}
		return builtinDefinition(definition.Name)
	}
	if definition.Kind != "registered" {
		return definition, unsupported("original native definition kind")
	}
	if definition.Name == "Agent" || definition.Name == "ExitPlanMode" || definition.Name == "ToolSearch" || strings.HasPrefix(definition.Name, "mcp__") {
		return definition, unsupported("swarm/plan/tool-search/MCP definition")
	}
	if _, err := protocol.DecodeJavaScriptJSONString(definition.Prompt); err != nil {
		return definition, unsupported("registered prompt callback")
	}
	if definition.InputSchema == nil {
		definition.InputSchema = &Schema{Kind: "object", UnknownKeys: "strip"}
	}
	if value := record.Get("inputSchema"); value != nil && string(value) != "null" {
		if err := checkSchemaDeclaration(value); err != nil {
			return definition, err
		}
	}
	if err := validateSchema(*definition.InputSchema); err != nil {
		return definition, err
	}
	if len(definition.InputJSONSchema) > 0 && string(definition.InputJSONSchema) != "null" {
		original, err := parseObject(definition.InputJSONSchema)
		if err != nil || !original.IsObject() {
			return definition, unsupported("original inputJSONSchema must be an object")
		}
	}
	return definition, nil
}

func scalarName(value json.RawMessage) error {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return unsupported("original definition name must be a string")
	}
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if unit >= 0xd800 && unit <= 0xdbff {
			if index+1 == len(units) || units[index+1] < 0xdc00 || units[index+1] > 0xdfff {
				return unsupported("unpaired UTF16 definition name")
			}
			index++
		} else if unit >= 0xdc00 && unit <= 0xdfff {
			return unsupported("unpaired UTF16 definition name")
		}
	}
	return nil
}
func checkSchemaDeclaration(value json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil || fields == nil {
		return unsupported("original schema declaration must be an object")
	}
	allowed := map[string]bool{"kind": true, "unknownKeys": true, "fields": true, "optional": true, "description": true, "default": true, "minimum": true, "exclusiveMinimum": true, "integer": true, "values": true}
	for key := range fields {
		if !allowed[key] {
			return unsupported("unported original schema field " + key)
		}
	}
	var children []map[string]json.RawMessage
	if source := fields["fields"]; source != nil {
		if err := json.Unmarshal(source, &children); err != nil {
			return unsupported("original schema fields declaration")
		}
		for _, child := range children {
			if len(child) != 2 || child["name"] == nil || child["schema"] == nil {
				return unsupported("original schema field descriptor")
			}
			if err := scalarName(child["name"]); err != nil {
				return err
			}
			if err := checkSchemaDeclaration(child["schema"]); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateSchema(schema Schema) error {
	if (schema.Pattern != "" || schema.Maximum != nil || schema.Items != nil || schema.MinimumLength != nil || schema.MaximumLength != nil || schema.MinimumItems != nil || schema.MaximumItems != nil || schema.RecordValues != nil || schema.Members != nil || schema.Literal != nil) && !schema.Native {
		return unsupported("unported registered array or string length recipe")
	}
	if schema.Kind != "number" && (schema.Integer || schema.Minimum != nil || schema.ExclusiveMinimum != nil) {
		return unsupported("numeric modifiers on a non-number declaration")
	}
	switch schema.Kind {
	case "object":
		if schema.UnknownKeys != "" && schema.UnknownKeys != "strip" && schema.UnknownKeys != "strict" && schema.UnknownKeys != "passthrough" {
			return unsupported("object unknown-key policy")
		}
		seen := map[string]bool{}
		for _, field := range schema.Fields {
			if seen[field.Name] {
				return unsupported("duplicate original schema field")
			}
			seen[field.Name] = true
			if err := validateSchema(field.Schema); err != nil {
				return err
			}
		}
	case "string", "boolean", "number":
	case "unknown":
		if !schema.Native {
			return unsupported("unported registered unknown declaration")
		}
	case "array":
		if !schema.Native || schema.Items == nil {
			return unsupported("unported registered array declaration")
		}
		if err := validateSchema(*schema.Items); err != nil {
			return err
		}
	case "enum":
		if len(schema.Values) == 0 {
			return unsupported("empty enum declaration")
		}
	case "record", "literal":
		if !schema.Native {
			return unsupported("unported registered record/literal declaration")
		}
		if schema.RecordValues != nil {
			if err := validateSchema(*schema.RecordValues); err != nil {
				return err
			}
		}
	case "union":
		if !schema.Native || len(schema.Members) == 0 {
			return unsupported("unported registered union declaration")
		}
		for _, member := range schema.Members {
			if err := validateSchema(member); err != nil {
				return err
			}
		}
	default:
		return unsupported("original schema kind " + schema.Kind)
	}
	if len(schema.Description) > 0 {
		if _, err := protocol.DecodeJavaScriptJSONString(schema.Description); err != nil {
			return unsupported("schema description must be a string")
		}
	}
	return nil
}

// Zod receives an ordinary JavaScript shape object. Its integer-index keys
// enumerate first even when the original declaration lists them later. Reuse
// the shared ordered-record boundary for that order, including required/issues.
func shapeFields(fields []Field) []Field {
	indices := protocol.NewJSONObject()
	for index, field := range fields {
		indices.Set(field.Name, array(index))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw(indices)))
	_, _ = decoder.Token()
	ordered := make([]Field, 0, len(fields))
	for decoder.More() {
		_, _ = decoder.Token()
		var index int
		_ = decoder.Decode(&index)
		ordered = append(ordered, fields[index])
	}
	return ordered
}

func compileSchema(schema Schema, root bool) (json.RawMessage, error) {
	if err := validateSchema(schema); err != nil {
		return nil, err
	}
	result := protocol.NewJSONObject()
	if root {
		result.Set("$schema", quote("https://json-schema.org/draft/2020-12/schema"))
	}
	// Zod optional/default/pipe wrappers put the outer description before the
	// flattened inner shape. Required plain primitives append it afterwards.
	descriptionFirst := schema.Optional || len(schema.Default) > 0 || schema.Semantic
	if len(schema.Default) > 0 && !schema.Optional && !schema.Semantic {
		result.Set("default", schema.Default)
	}
	if descriptionFirst {
		result.Set("description", schema.Description)
	}
	result.Set("default", schema.Default)
	if schema.Kind == "array" {
		if schema.MinimumItems != nil {
			result.Set("minItems", array(*schema.MinimumItems))
		}
		if schema.MaximumItems != nil {
			result.Set("maxItems", array(*schema.MaximumItems))
		}
	}
	kind := schema.Kind
	if kind == "enum" || kind == "literal" {
		kind = "string"
	}
	if kind == "record" {
		kind = "object"
	}
	if kind == "number" && schema.Integer {
		kind = "integer"
	}
	if schema.Kind == "union" {
		members := []json.RawMessage{}
		for _, member := range schema.Members {
			value, err := compileSchema(member, false)
			if err != nil {
				return nil, err
			}
			members = append(members, value)
		}
		keyword := "anyOf"
		if schema.Discriminator != "" {
			keyword = "oneOf"
		}
		result.Set(keyword, array(members))
	} else if schema.Kind != "unknown" {
		result.Set("type", quote(kind))
	}
	if schema.Kind == "record" {
		result.Set("propertyNames", raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("string")})))
		if schema.RecordValues != nil {
			value, err := compileSchema(*schema.RecordValues, false)
			if err != nil {
				return nil, err
			}
			result.Set("additionalProperties", value)
		} else {
			result.Set("additionalProperties", json.RawMessage("{}"))
		}
	}
	if schema.Kind == "literal" {
		result.Set("const", schema.Literal)
	}
	if schema.Kind == "array" {
		items, err := compileSchema(*schema.Items, false)
		if err != nil {
			return nil, err
		}
		result.Set("items", items)
	}
	if schema.Format != "" {
		result.Set("format", quote(schema.Format))
	}
	if schema.MinimumLength != nil {
		result.Set("minLength", array(*schema.MinimumLength))
	}
	if schema.MaximumLength != nil {
		result.Set("maxLength", array(*schema.MaximumLength))
	}
	if schema.Pattern != "" {
		result.Set("pattern", quote(schema.Pattern))
	}
	if schema.Kind == "object" {
		properties := protocol.NewJSONObject()
		required := []string{}
		for _, field := range shapeFields(schema.Fields) {
			value, err := compileSchema(field.Schema, false)
			if err != nil {
				return nil, err
			}
			properties.Set(field.Name, value)
			if !field.Schema.Optional {
				required = append(required, field.Name)
			}
		}
		result.Set("properties", raw(properties))
		if len(required) > 0 {
			result.Set("required", array(required))
		}
		if schema.UnknownKeys != "passthrough" {
			result.Set("additionalProperties", json.RawMessage("false"))
		} else {
			result.Set("additionalProperties", json.RawMessage("{}"))
		}
	}
	if schema.Minimum != nil {
		result.Set("minimum", array(*schema.Minimum))
	}
	if schema.Maximum != nil {
		result.Set("maximum", array(*schema.Maximum))
	}
	if schema.ExclusiveMinimum != nil {
		result.Set("exclusiveMinimum", array(*schema.ExclusiveMinimum))
	}
	if schema.Integer {
		if schema.Minimum == nil && schema.ExclusiveMinimum == nil {
			result.Set("minimum", json.RawMessage("-9007199254740991"))
		}
		if schema.Maximum == nil || *schema.Maximum > 9007199254740991 {
			result.Set("maximum", json.RawMessage("9007199254740991"))
		}
	}
	if schema.Kind == "enum" {
		result.Set("enum", array(schema.Values))
	}
	if !descriptionFirst {
		result.Set("description", schema.Description)
	}
	return raw(result), nil
}
func MatchesName(definition Definition, name string) bool {
	if definition.Name == name {
		return true
	}
	for _, alias := range definition.Aliases {
		if alias == name {
			return true
		}
	}
	return false
}
func CanonicalName(definitions []Definition, name string) string {
	for _, definition := range definitions {
		if MatchesName(definition, name) {
			return definition.Name
		}
	}
	return name
}

// NormalizeToolUse compares JavaScript string identities before decoding to a
// Go string. A lone surrogate must not match an alias containing U+FFFD.
func NormalizeToolUse(definitions []Definition, name, input json.RawMessage) (json.RawMessage, json.RawMessage) {
	identity, err := stringify(name)
	if err != nil || len(identity) == 0 || identity[0] != '"' {
		return name, input
	}
	for _, definition := range definitions {
		names := append([]string{definition.Name}, definition.Aliases...)
		for _, candidate := range names {
			candidateIdentity, _ := stringify(quote(candidate))
			if string(identity) == string(candidateIdentity) {
				return quote(definition.Name), NormalizeAPIInput(definition.Name, input)
			}
		}
	}
	return name, input
}
func NormalizeAPIInput(name string, input json.RawMessage) json.RawMessage {
	if name == "ExitPlanMode" {
		object, err := parseObject(input)
		if err != nil || !object.IsObject() || object.Get("plan") == nil && object.Get("planFilePath") == nil {
			return input
		}
		object.Delete("plan")
		object.Delete("planFilePath")
		return raw(object)
	}
	if name != "Edit" {
		return input
	}
	object, err := parseObject(input)
	if err != nil || !object.IsObject() || object.Get("edits") == nil {
		return input
	}
	for _, field := range []string{"old_string", "new_string", "replace_all"} {
		object.Delete(field)
	}
	return raw(object)
}
func structuredModel(model string) bool {
	model = strings.ToLower(model)
	for _, name := range []string{"claude-fable-5", "claude-sonnet-5", "claude-sonnet-4-6", "claude-sonnet-4-5", "claude-opus-4-1", "claude-opus-4-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-haiku-4-5"} {
		if strings.Contains(model, name) {
			return true
		}
	}
	return false
}
