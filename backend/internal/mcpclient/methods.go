package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (c *Client) method(ctx context.Context, name string, params protocol.JSONObject, options RequestOptions) (protocol.JSONObject, error) {
	result, err := c.Request(ctx, name, params, options)
	if err != nil {
		return result, err
	}
	return validateResult(name, result)
}
func cursorParams(cursor *string) protocol.JSONObject {
	if cursor == nil {
		return protocol.JSONObject{}
	}
	return object(field("cursor", *cursor))
}

// Lists return exactly one server page. The original fetchers do not silently
// follow nextCursor; callers can request additional pages explicitly.
func (c *Client) ListTools(ctx context.Context, cursor *string) (protocol.JSONObject, error) {
	return c.method(ctx, "tools/list", cursorParams(cursor), RequestOptions{})
}
func (c *Client) ListResources(ctx context.Context, cursor *string) (protocol.JSONObject, error) {
	return c.method(ctx, "resources/list", cursorParams(cursor), RequestOptions{})
}
func (c *Client) ListResourceTemplates(ctx context.Context, cursor *string) (protocol.JSONObject, error) {
	return c.method(ctx, "resources/templates/list", cursorParams(cursor), RequestOptions{})
}
func (c *Client) ReadResource(ctx context.Context, uri string) (protocol.JSONObject, error) {
	return c.method(ctx, "resources/read", object(field("uri", uri)), RequestOptions{})
}
func (c *Client) Subscribe(ctx context.Context, uri string) (protocol.JSONObject, error) {
	return c.method(ctx, "resources/subscribe", object(field("uri", uri)), RequestOptions{})
}
func (c *Client) Unsubscribe(ctx context.Context, uri string) (protocol.JSONObject, error) {
	return c.method(ctx, "resources/unsubscribe", object(field("uri", uri)), RequestOptions{})
}
func (c *Client) ListPrompts(ctx context.Context, cursor *string) (protocol.JSONObject, error) {
	return c.method(ctx, "prompts/list", cursorParams(cursor), RequestOptions{})
}
func (c *Client) GetPrompt(ctx context.Context, name string, arguments protocol.JSONObject) (protocol.JSONObject, error) {
	params := object(field("name", name))
	if arguments.IsObject() {
		params.Set("arguments", raw(arguments))
	}
	return c.method(ctx, "prompts/get", params, RequestOptions{})
}
func (c *Client) CallTool(ctx context.Context, name string, arguments, meta protocol.JSONObject, options RequestOptions) (protocol.JSONObject, error) {
	return c.CallToolJSON(ctx, raw(name), arguments, meta, options)
}

func (c *Client) CallToolJSON(ctx context.Context, name json.RawMessage, arguments, meta protocol.JSONObject, options RequestOptions) (protocol.JSONObject, error) {
	if _, err := protocol.DecodeJavaScriptJSONString(name); err != nil {
		return protocol.JSONObject{}, err
	}
	params := object()
	params.Set("name", name)
	if arguments.IsObject() {
		params.Set("arguments", raw(arguments))
	}
	if meta.IsObject() {
		params.Set("_meta", raw(meta))
	}
	if options.Timeout <= 0 {
		options.Timeout = c.options.ToolTimeout
	}
	return c.method(ctx, "tools/call", params, options)
}
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.method(ctx, "ping", protocol.JSONObject{}, RequestOptions{})
	return err
}

func NormalizeName(name string) string {
	var b strings.Builder
	for _, u := range utf16.Encode([]rune(name)) {
		if u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9' || u == '_' || u == '-' {
			b.WriteByte(byte(u))
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if strings.HasPrefix(name, "claude.ai ") {
		for strings.Contains(s, "__") {
			s = strings.ReplaceAll(s, "__", "_")
		}
		s = strings.Trim(s, "_")
	}
	return s
}
func ToolName(server, name string) string {
	return "mcp__" + NormalizeName(server) + "__" + NormalizeName(name)
}

// TruncateDescription keeps JavaScript UTF-16 slicing, including a lone
// surrogate at the cut, rather than Go rune or byte slicing.
func TruncateDescription(value json.RawMessage, limit int) json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return value
	}
	if limit <= 0 {
		limit = 2048
	}
	if len(units) <= limit {
		return value
	}
	units = append(units[:limit:limit], utf16.Encode([]rune("… [truncated]"))...)
	return protocol.QuoteJavaScriptUTF16(units)
}

type ResultValidationError struct {
	Path     string
	Expected string
}

func (e *ResultValidationError) Error() string {
	return fmt.Sprintf("Invalid MCP result at %s: expected %s", e.Path, e.Expected)
}
func invalid(path, expected string) error { return &ResultValidationError{path, expected} }

func fields(o protocol.JSONObject) []protocol.JSONField {
	properties := o.OwnProperties()
	values := make([]protocol.JSONField, 0, len(properties))
	for _, p := range properties {
		var name string
		_ = json.Unmarshal(p.NameJSON, &name)
		values = append(values, protocol.JSONField{Name: name, Value: p.Value})
	}
	return values
}

// Zod reconstructs declared fields in schema order, then copies loose/catchall
// fields. Preserve that order because tool schemas are later model-visible.
func ordered(o protocol.JSONObject, names ...string) protocol.JSONObject {
	out := object()
	known := map[string]bool{}
	for _, name := range names {
		known[name] = true
		if v := o.Get(name); v != nil {
			out.Set(name, v)
		}
	}
	for _, p := range o.OwnProperties() {
		var name string
		_ = json.Unmarshal(p.NameJSON, &name)
		if !known[name] {
			_ = out.SetPropertyJSON(p.NameJSON, p.Value)
		}
	}
	return out
}

type shape struct {
	required      map[string]string
	optional      map[string]string
	retainUnknown bool
}

func stringFields(names ...string) map[string]string {
	m := map[string]string{}
	for _, name := range names {
		m[name] = "string"
	}
	return m
}
func validateObject(o protocol.JSONObject, s shape, path string) (protocol.JSONObject, error) {
	if !o.IsObject() {
		return o, invalid(path, "object")
	}
	out := object()
	if s.retainUnknown {
		out = o.Clone()
	}
	for _, f := range fields(o) {
		typeName, known := s.required[f.Name]
		if !known {
			typeName, known = s.optional[f.Name]
		}
		if !known {
			continue
		}
		if !hasType(f.Value, typeName) {
			return out, invalid(path+"."+f.Name, typeName)
		}
		out.Set(f.Name, f.Value)
	}
	for name, typeName := range s.required {
		if o.Get(name) == nil {
			return out, invalid(path+"."+name, typeName)
		}
	}
	return out, nil
}
func hasType(b json.RawMessage, name string) bool {
	b = json.RawMessage(strings.TrimSpace(string(b)))
	if b == nil || string(b) == "null" {
		return false
	}
	switch name {
	case "string":
		var s string
		return json.Unmarshal(b, &s) == nil
	case "boolean":
		return string(b) == "true" || string(b) == "false"
	case "number":
		var n json.Number
		return json.Unmarshal(b, &n) == nil && len(b) > 0 && b[0] != '"'
	case "object":
		return nested(b).IsObject()
	case "objectLike":
		return nested(b).IsObject() || len(b) > 0 && b[0] == '[' && json.Valid(b)
	case "array":
		var a []json.RawMessage
		return len(b) > 0 && b[0] == '[' && json.Unmarshal(b, &a) == nil
	}
	return false
}
func validateInitialize(o protocol.JSONObject) error {
	_, err := validateObject(o, shape{required: map[string]string{"protocolVersion": "string", "capabilities": "object", "serverInfo": "object"}, optional: map[string]string{"instructions": "string", "_meta": "object"}, retainUnknown: true}, "initialize")
	if err != nil {
		return err
	}
	_, err = validateObject(nested(o.Get("serverInfo")), shape{required: stringFields("name", "version"), optional: stringFields("title", "description", "websiteUrl"), retainUnknown: true}, "initialize.serverInfo")
	return err
}

func normalizeInitialize(o protocol.JSONObject) (protocol.JSONObject, error) {
	if err := validateInitialize(o); err != nil {
		return o, err
	}
	out := o.Clone()
	info, err := validateObject(nested(o.Get("serverInfo")), shape{required: stringFields("name", "version"), optional: map[string]string{"title": "string", "description": "string", "websiteUrl": "string", "icons": "array"}}, "initialize.serverInfo")
	if err != nil {
		return out, err
	}
	info, err = validateIconsAndAnnotations(info, "initialize.serverInfo")
	if err != nil {
		return out, err
	}
	out.Set("serverInfo", raw(ordered(info, "name", "title", "icons", "version", "websiteUrl", "description")))
	caps, err := validateObject(nested(o.Get("capabilities")), shape{optional: map[string]string{"experimental": "object", "logging": "objectLike", "completions": "objectLike", "prompts": "object", "resources": "object", "tools": "object", "tasks": "object", "extensions": "object"}}, "initialize.capabilities")
	if err != nil {
		return out, err
	}
	for _, name := range []string{"prompts", "resources", "tools"} {
		if v := caps.Get(name); v != nil {
			optional := map[string]string{"listChanged": "boolean"}
			if name == "resources" {
				optional["subscribe"] = "boolean"
			}
			value, err := validateObject(nested(v), shape{optional: optional}, "initialize.capabilities."+name)
			if err != nil {
				return out, err
			}
			caps.Set(name, raw(value))
		}
	}
	for _, name := range []string{"experimental", "extensions"} {
		if v := caps.Get(name); v != nil {
			for _, f := range fields(nested(v)) {
				if !hasType(f.Value, "objectLike") {
					return out, invalid("initialize.capabilities."+name+"."+f.Name, "object")
				}
			}
		}
	}
	if v := caps.Get("tasks"); v != nil {
		tasks, err := validateObject(nested(v), shape{optional: map[string]string{"list": "objectLike", "cancel": "objectLike", "requests": "object"}, retainUnknown: true}, "initialize.capabilities.tasks")
		if err != nil {
			return out, err
		}
		if v := tasks.Get("requests"); v != nil {
			requests, err := validateObject(nested(v), shape{optional: map[string]string{"tools": "object"}, retainUnknown: true}, "initialize.capabilities.tasks.requests")
			if err != nil {
				return out, err
			}
			if v := requests.Get("tools"); v != nil {
				tools, err := validateObject(nested(v), shape{optional: map[string]string{"call": "objectLike"}, retainUnknown: true}, "initialize.capabilities.tasks.requests.tools")
				if err != nil {
					return out, err
				}
				requests.Set("tools", raw(ordered(tools, "call")))
			}
			tasks.Set("requests", raw(ordered(requests, "tools")))
		}
		caps.Set("tasks", raw(ordered(tasks, "list", "cancel", "requests")))
	}
	out.Set("capabilities", raw(ordered(caps, "experimental", "logging", "completions", "prompts", "resources", "tools", "tasks", "extensions")))
	return ordered(out, "_meta", "protocolVersion", "capabilities", "serverInfo", "instructions"), nil
}

func validateIconsAndAnnotations(o protocol.JSONObject, path string) (protocol.JSONObject, error) {
	if v := o.Get("icons"); v != nil {
		var icons []json.RawMessage
		if json.Unmarshal(v, &icons) != nil || !hasType(v, "array") {
			return o, invalid(path+".icons", "array")
		}
		values := []protocol.JSONObject{}
		for i, item := range icons {
			p := fmt.Sprintf("%s.icons[%d]", path, i)
			icon, err := validateObject(nested(item), shape{required: stringFields("src"), optional: map[string]string{"mimeType": "string", "sizes": "array", "theme": "string"}}, p)
			if err != nil {
				return o, err
			}
			if v := icon.Get("theme"); v != nil && icon.String("theme") != "dark" && icon.String("theme") != "light" {
				return o, invalid(p+".theme", "light or dark")
			}
			if v := icon.Get("sizes"); v != nil {
				var sizes []json.RawMessage
				_ = json.Unmarshal(v, &sizes)
				for _, size := range sizes {
					if !hasType(size, "string") {
						return o, invalid(p+".sizes", "string array")
					}
				}
			}
			values = append(values, ordered(icon, "src", "mimeType", "sizes", "theme"))
		}
		o.Set("icons", raw(values))
	}
	if v := o.Get("annotations"); v != nil {
		annotations, err := validateObject(nested(v), shape{optional: map[string]string{"audience": "array", "priority": "number", "lastModified": "string"}}, path+".annotations")
		if err != nil {
			return o, err
		}
		if v := annotations.Get("audience"); v != nil {
			var audience []string
			if json.Unmarshal(v, &audience) != nil {
				return o, invalid(path+".annotations.audience", "role array")
			}
			for _, role := range audience {
				if role != "user" && role != "assistant" {
					return o, invalid(path+".annotations.audience", "role array")
				}
			}
		}
		if v := annotations.Get("priority"); v != nil {
			var priority float64
			_ = json.Unmarshal(v, &priority)
			if priority < 0 || priority > 1 {
				return o, invalid(path+".annotations.priority", "number between 0 and 1")
			}
		}
		if v := annotations.Get("lastModified"); v != nil {
			if _, err := time.Parse(time.RFC3339Nano, annotations.String("lastModified")); err != nil {
				return o, invalid(path+".annotations.lastModified", "ISO datetime")
			}
		}
		o.Set("annotations", raw(ordered(annotations, "audience", "priority", "lastModified")))
	}
	return o, nil
}
func validBase64(value string) bool {
	value = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\f' {
			return -1
		}
		return r
	}, value)
	if strings.Contains(value, "=") {
		_, err := base64.StdEncoding.DecodeString(value)
		return err == nil
	}
	_, err := base64.RawStdEncoding.DecodeString(value)
	return err == nil
}
func validateResult(method string, o protocol.JSONObject) (protocol.JSONObject, error) {
	if !o.IsObject() {
		return o, invalid(method, "object")
	}
	if v := o.Get("_meta"); v != nil && !hasType(v, "object") {
		return o, invalid(method+"._meta", "object")
	}
	out := o.Clone()
	if v := o.Get("_meta"); v != nil {
		meta := nested(v).Clone()
		if token := meta.Get("progressToken"); token != nil {
			var n json.Number
			if !hasType(token, "string") && (json.Unmarshal(token, &n) != nil || !integerNumber(n)) {
				return out, invalid(method+"._meta.progressToken", "progress token")
			}
		}
		if task := meta.Get("io.modelcontextprotocol/related-task"); task != nil {
			normalized, err := validateObject(nested(task), shape{required: stringFields("taskId")}, method+"._meta.io.modelcontextprotocol/related-task")
			if err != nil {
				return out, err
			}
			meta.Set("io.modelcontextprotocol/related-task", raw(normalized))
		}
		out.Set("_meta", raw(ordered(meta, "progressToken", "io.modelcontextprotocol/related-task")))
	}
	if v := o.Get("nextCursor"); v != nil && !hasType(v, "string") {
		return out, invalid(method+".nextCursor", "string")
	}
	var listField string
	var entry func(protocol.JSONObject, string) (protocol.JSONObject, error)
	switch method {
	case "initialize":
		return normalizeInitialize(out)
	case "tools/list":
		listField = "tools"
		entry = validateTool
	case "resources/list":
		listField = "resources"
		entry = validateResource
	case "resources/templates/list":
		listField = "resourceTemplates"
		entry = validateTemplate
	case "prompts/list":
		listField = "prompts"
		entry = validatePrompt
	case "resources/read":
		listField = "contents"
		entry = validateResourceContents
	case "prompts/get":
		if v := o.Get("description"); v != nil && !hasType(v, "string") {
			return out, invalid(method+".description", "string")
		}
		listField = "messages"
		entry = validatePromptMessage
	case "tools/call":
		if o.Get("content") == nil {
			out.Set("content", raw([]any{}))
			o = out
		}
		if v := o.Get("isError"); v != nil && !hasType(v, "boolean") {
			return out, invalid(method+".isError", "boolean")
		}
		if v := o.Get("structuredContent"); v != nil && !hasType(v, "object") {
			return out, invalid(method+".structuredContent", "object")
		}
		listField = "content"
		entry = validateContent
	case "ping", "resources/subscribe", "resources/unsubscribe":
		for _, f := range fields(o) {
			if f.Name != "_meta" {
				return out, invalid(method, "empty result")
			}
		}
		return ordered(out, "_meta"), nil
	default:
		return out, nil
	}
	if !hasType(o.Get(listField), "array") {
		return out, invalid(method+"."+listField, "array")
	}
	var items []json.RawMessage
	_ = json.Unmarshal(o.Get(listField), &items)
	values := make([]protocol.JSONObject, 0, len(items))
	for i, item := range items {
		value, err := entry(nested(item), fmt.Sprintf("%s.%s[%d]", method, listField, i))
		if err != nil {
			return out, err
		}
		values = append(values, value)
	}
	out.Set(listField, raw(values))
	switch method {
	case "tools/list", "resources/list", "resources/templates/list", "prompts/list":
		return ordered(out, "_meta", "nextCursor", listField), nil
	case "tools/call":
		return ordered(out, "_meta", "content", "structuredContent", "isError"), nil
	case "prompts/get":
		return ordered(out, "_meta", "description", "messages"), nil
	case "resources/read":
		return ordered(out, "_meta", "contents"), nil
	}
	return ordered(out, "_meta"), nil
}

var metadataOptional = map[string]string{"title": "string", "description": "string", "icons": "array", "_meta": "object"}

func validateTool(o protocol.JSONObject, path string) (protocol.JSONObject, error) {
	optional := map[string]string{}
	for k, v := range metadataOptional {
		optional[k] = v
	}
	optional["outputSchema"] = "object"
	optional["annotations"] = "object"
	optional["execution"] = "object"
	out, err := validateObject(o, shape{required: map[string]string{"name": "string", "inputSchema": "object"}, optional: optional}, path)
	if err != nil {
		return out, err
	}
	for _, name := range []string{"inputSchema", "outputSchema"} {
		if o.Get(name) == nil {
			continue
		}
		schema := nested(o.Get(name))
		if schema.String("type") != "object" {
			return out, invalid(path+"."+name+".type", "object literal")
		}
		if v := schema.Get("properties"); v != nil {
			properties := nested(v)
			if !properties.IsObject() {
				return out, invalid(path+"."+name+".properties", "object")
			}
			for _, f := range fields(properties) {
				if !hasType(f.Value, "objectLike") {
					return out, invalid(path+"."+name+".properties."+f.Name, "object")
				}
			}
		}
		if v := schema.Get("required"); v != nil {
			if !hasType(v, "array") {
				return out, invalid(path+"."+name+".required", "array")
			}
			var items []json.RawMessage
			_ = json.Unmarshal(v, &items)
			for i, item := range items {
				if !hasType(item, "string") {
					return out, invalid(fmt.Sprintf("%s.%s.required[%d]", path, name, i), "string")
				}
			}
		}
		out.Set(name, raw(ordered(schema, "type", "properties", "required")))
	}
	if v := o.Get("annotations"); v != nil {
		annotation, err := validateObject(nested(v), shape{optional: map[string]string{"title": "string", "readOnlyHint": "boolean", "destructiveHint": "boolean", "idempotentHint": "boolean", "openWorldHint": "boolean"}}, path+".annotations")
		if err != nil {
			return out, err
		}
		out.Set("annotations", raw(ordered(annotation, "title", "readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint")))
	}
	if v := o.Get("execution"); v != nil {
		execution, err := validateObject(nested(v), shape{optional: map[string]string{"taskSupport": "string"}}, path+".execution")
		if err != nil {
			return out, err
		}
		if x := execution.Get("taskSupport"); x != nil && execution.String("taskSupport") != "optional" && execution.String("taskSupport") != "required" && execution.String("taskSupport") != "forbidden" {
			return out, invalid(path+".execution.taskSupport", "task support enum")
		}
		out.Set("execution", raw(execution))
	}
	// Tool annotations have their own hint schema, so only icons use the common
	// resource annotation validator here.
	annotations := out.Get("annotations")
	out.Delete("annotations")
	out, err = validateIconsAndAnnotations(out, path)
	if annotations != nil {
		out.Set("annotations", annotations)
	}
	return ordered(out, "name", "title", "icons", "description", "inputSchema", "outputSchema", "annotations", "execution", "_meta"), err
}
func validateResource(o protocol.JSONObject, path string) (protocol.JSONObject, error) {
	optional := map[string]string{}
	for k, v := range metadataOptional {
		optional[k] = v
	}
	optional["mimeType"] = "string"
	optional["size"] = "number"
	optional["annotations"] = "object"
	out, err := validateObject(o, shape{required: stringFields("name", "uri"), optional: optional}, path)
	if err != nil {
		return out, err
	}
	out, err = validateIconsAndAnnotations(out, path)
	return ordered(out, "name", "title", "icons", "uri", "description", "mimeType", "size", "annotations", "_meta"), err
}
func validateTemplate(o protocol.JSONObject, path string) (protocol.JSONObject, error) {
	optional := map[string]string{}
	for k, v := range metadataOptional {
		optional[k] = v
	}
	optional["mimeType"] = "string"
	optional["annotations"] = "object"
	out, err := validateObject(o, shape{required: stringFields("name", "uriTemplate"), optional: optional}, path)
	if err != nil {
		return out, err
	}
	out, err = validateIconsAndAnnotations(out, path)
	return ordered(out, "name", "title", "icons", "uriTemplate", "description", "mimeType", "annotations", "_meta"), err
}
func validatePrompt(o protocol.JSONObject, path string) (protocol.JSONObject, error) {
	optional := map[string]string{}
	for k, v := range metadataOptional {
		optional[k] = v
	}
	optional["arguments"] = "array"
	out, err := validateObject(o, shape{required: stringFields("name"), optional: optional}, path)
	if err != nil {
		return out, err
	}
	if v := out.Get("arguments"); v != nil {
		var items []json.RawMessage
		_ = json.Unmarshal(v, &items)
		values := make([]protocol.JSONObject, 0, len(items))
		for i, item := range items {
			value, err := validateObject(nested(item), shape{required: stringFields("name"), optional: map[string]string{"description": "string", "required": "boolean"}}, fmt.Sprintf("%s.arguments[%d]", path, i))
			if err != nil {
				return out, err
			}
			values = append(values, ordered(value, "name", "description", "required"))
		}
		out.Set("arguments", raw(values))
	}
	out, err = validateIconsAndAnnotations(out, path)
	return ordered(out, "name", "title", "icons", "description", "arguments", "_meta"), err
}
func validateResourceContents(o protocol.JSONObject, path string) (protocol.JSONObject, error) {
	required := stringFields("uri", "text")
	if !hasType(o.Get("text"), "string") {
		required = stringFields("uri", "blob")
	}
	out, err := validateObject(o, shape{required: required, optional: map[string]string{"mimeType": "string", "_meta": "object"}}, path)
	if err == nil && out.Get("blob") != nil && !validBase64(out.String("blob")) {
		return out, invalid(path+".blob", "Base64 string")
	}
	return ordered(out, "uri", "mimeType", "_meta", "text", "blob"), err
}
func validateContent(o protocol.JSONObject, path string) (protocol.JSONObject, error) {
	var required map[string]string
	optional := map[string]string{"annotations": "object", "_meta": "object"}
	switch o.String("type") {
	case "text":
		required = stringFields("type", "text")
	case "image", "audio":
		required = stringFields("type", "data", "mimeType")
	case "resource":
		required = map[string]string{"type": "string", "resource": "object"}
	case "resource_link":
		out, err := validateResource(o, path)
		if err == nil {
			out.Set("type", raw("resource_link"))
		}
		return ordered(out, "name", "title", "icons", "uri", "description", "mimeType", "size", "annotations", "_meta", "type"), err
	default:
		return o, invalid(path+".type", "content type enum")
	}
	out, err := validateObject(o, shape{required: required, optional: optional}, path)
	if err != nil {
		return out, err
	}
	if o.String("type") == "resource" {
		resource, err := validateResourceContents(nested(o.Get("resource")), path+".resource")
		if err != nil {
			return out, err
		}
		out.Set("resource", raw(resource))
	}
	if o.String("type") == "image" || o.String("type") == "audio" {
		if !validBase64(o.String("data")) {
			return out, invalid(path+".data", "Base64 string")
		}
	}
	out, err = validateIconsAndAnnotations(out, path)
	return ordered(out, "type", "text", "data", "mimeType", "resource", "annotations", "_meta"), err
}
func validatePromptMessage(o protocol.JSONObject, path string) (protocol.JSONObject, error) {
	out, err := validateObject(o, shape{required: map[string]string{"role": "string", "content": "object"}}, path)
	if err != nil {
		return out, err
	}
	if o.String("role") != "user" && o.String("role") != "assistant" {
		return out, invalid(path+".role", "user or assistant")
	}
	content, err := validateContent(nested(o.Get("content")), path+".content")
	if err == nil {
		out.Set("content", raw(content))
	}
	return ordered(out, "role", "content"), err
}
