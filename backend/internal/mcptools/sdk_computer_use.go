package mcptools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/computerusemcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

const ComputerUseServerName = "computer-use"

// RegisterComputerUse binds the actual in-process SDK server. This does not
// fabricate a connected stdio peer or start the descriptive setup command.
func (s *Service) RegisterComputerUse(scope *cooperative.Scope, binder *computerusemcp.Binder, configuration ...protocol.JSONObject) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	if binder == nil || s.options.ComputerUseEnabled == nil {
		return errors.New("Computer Use requires the actual shared SDK binder")
	}
	if s.computerUse != nil && s.computerUse != binder {
		return errors.New("Computer Use SDK binding already registered")
	}
	s.computerUse = binder
	if len(configuration) > 0 {
		s.computerUseConfig = configuration[0].Clone()
	}
	return nil
}

func (s *Service) computerUseCatalog(scope *cooperative.Scope) ([]toolschema.Definition, []queryloop.RegisteredTool, error) {
	if s.computerUse == nil {
		return nil, nil, nil
	}
	future, e := native(s, scope, func() ([]protocol.JSONObject, error) {
		enabled, e := s.options.ComputerUseEnabled(context.Background())
		if e != nil {
			return nil, e
		}
		if !enabled {
			return []protocol.JSONObject{}, nil
		}
		return s.computerUse.Tools(context.Background())
	})
	if e != nil {
		return nil, nil, e
	}
	metadata, e := future.Await(scope)
	if e != nil {
		return nil, nil, e
	}
	permissions, e := s.options.PermissionContext(scope)
	if e != nil {
		return nil, nil, e
	}
	defs := []toolschema.Definition{}
	registered := []queryloop.RegisteredTool{}
	for _, entry := range metadata {
		name := mcpclient.ToolName(ComputerUseServerName, entry.String("name"))
		var deny protocol.JSONObject
		_ = deny.UnmarshalJSON(permissions.Get("alwaysDenyRules"))
		denied := false
		for _, source := range deny.OwnProperties() {
			var rules []string
			_ = json.Unmarshal(source.Value, &rules)
			for _, rule := range rules {
				denied = denied || ruleMatches(rule, name, ComputerUseServerName)
			}
		}
		if denied {
			continue
		}
		prompt := entry.Get("description")
		if prompt == nil {
			prompt = raw("")
		}
		prompt = mcpclient.TruncateDescription(prompt, descriptionLimit(s.options.Environment))
		defs = append(defs, toolschema.Definition{IsMCP: true, Kind: "registered", Name: name, InputSchema: &toolschema.Schema{Kind: "object", UnknownKeys: "passthrough"}, InputJSONSchema: entry.Get("inputSchema"), Description: entry.Get("description"), Prompt: prompt})
		tool := queryloop.RegisteredTool{Name: name, IsMCP: true, PersistenceThreshold: 50000, ParseInput: func(input protocol.JSONObject) (protocol.JSONObject, error) { return input.Clone(), nil }, IsConcurrencySafe: func(protocol.JSONObject) bool { return false }, MapResultJSON: func(data, id json.RawMessage) (protocol.JSONObject, error) {
			return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "type", Value: raw("tool_result")}, protocol.JSONField{Name: "content", Value: data}), nil
		}}
		start := func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			scope := s.options.ScopeForContext(call.Opaque)
			return native(s, scope, func() (queryloop.ToolCallResult, error) {
				args, e := input.MarshalJSON()
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				enabled, e := s.options.ComputerUseEnabled(ctx)
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				if !enabled {
					return queryloop.ToolCallResult{}, errors.New("MCP server \"computer-use\" is disabled")
				}
				result := s.computerUse.Call(ctx, entry.String("name"), args)
				return ProjectComputerUseResult(result, entry.String("name"))
			})
		}
		tool.StartCallJSON = start
		tool.CallJSON = func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
			future, e := start(ctx, input, call)
			if e != nil {
				return queryloop.ToolCallResult{}, e
			}
			return future.Await(s.options.ScopeForContext(call.Opaque))
		}
		tool, e = queryloop.BindNativeMCP(tool, s.options.Runtime)
		if e != nil {
			return nil, nil, e
		}
		registered = append(registered, tool)
	}
	return defs, registered, nil
}

// EndTurnOwner is an actual native cleanup promise on the existing Agent owner.
// Normal completion and cancellation keep the isolated JS bindings, but release
// the native turn/lease. Closing the host additionally closes its worker.
func (s *Service) EndTurnOwner(scope *cooperative.Scope) error {
	return s.EndTurnOwnerContext(scope, context.Background())
}

func (s *Service) EndTurnOwnerContext(scope *cooperative.Scope, cleanupContext context.Context) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	if cleanupContext == nil {
		return cooperative.ErrRestricted
	}
	if s.computerUse == nil {
		return s.endInactiveComputerUseTurn(scope)
	}
	future, e := native(s, scope, func() (struct{}, error) { return struct{}{}, s.computerUse.EndTurn(cleanupContext) })
	if e != nil {
		return e
	}
	_, e = future.Await(scope)
	return e
}
func (s *Service) HasComputerUse() bool { return s != nil && s.computerUse != nil }

func encodedImage(data string) []byte {
	// Buffer.from(base64) accepts whitespace, URL-safe characters and omitted
	// padding. Header inspection leaves the delivered original base64 unchanged.
	var clean strings.Builder
	for _, c := range data {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+' || c == '/':
			clean.WriteRune(c)
		case c == '-':
			clean.WriteByte('+')
		case c == '_':
			clean.WriteByte('/')
		case c == '=':
			goto decode
		}
	}
decode:
	encoded := clean.String()
	if len(encoded)%4 == 1 {
		encoded = encoded[:len(encoded)-1]
	}
	decoded, _ := base64.RawStdEncoding.DecodeString(encoded)
	return decoded
}
func computerUseCaption(data string, number int, zoom bool) string {
	config, _, e := image.DecodeConfig(bytes.NewReader(encodedImage(data)))
	if e != nil || config.Width < 1 || config.Height < 1 {
		return ""
	}
	caption := fmt.Sprintf("\nImage %d: %d×%d pixels. Image pixel bounds: top-left (0,0), bottom-right (%d,%d).", number, config.Width, config.Height, config.Width-1, config.Height-1)
	if zoom {
		caption += " Zoom image for inspection; action coordinates still refer to the full-screen screenshot in the tool's documented units."
	}
	return caption + "\n"
}

// ProjectComputerUseResult ports the Source CLI .call override. Telemetry is
// excluded; native errors become the ordinary Harness error path. Encoded image
// headers are read without resizing, reencoding or dropping a completed action.
func ProjectComputerUseResult(result curepl.Result, toolName string) (queryloop.ToolCallResult, error) {
	encoded, e := json.Marshal(result)
	if e != nil {
		return queryloop.ToolCallResult{}, e
	}
	var original protocol.JSONObject
	if e := original.UnmarshalJSON(encoded); e != nil {
		return queryloop.ToolCallResult{}, e
	}
	return ProjectComputerUseJSONResult(original, toolName)
}

func truthyMCPJSON(value json.RawMessage) bool {
	if len(bytes.TrimSpace(value)) == 0 {
		return false
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return false
	}
	switch v := decoded.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}

// ProjectComputerUseJSONResult retains the original worker metadata property
// order through Source's model-visible JSON.stringify text projection.
func ProjectComputerUseJSONResult(original protocol.JSONObject, toolName string) (queryloop.ToolCallResult, error) {
	content := []protocol.JSONObject{}
	if structured := original.Get("structuredContent"); truthyMCPJSON(structured) {
		value, e := protocol.ParseAndStringifyJavaScriptJSON(structured)
		if e != nil {
			return queryloop.ToolCallResult{}, e
		}
		content = append(content, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("text")}, protocol.JSONField{Name: "text", Value: raw(string(value))}))
	}
	var supplied []protocol.JSONObject
	_ = json.Unmarshal(original.Get("content"), &supplied)
	content = append(content, supplied...)
	if string(original.Get("isError")) == "true" {
		messages := []string{}
		for _, item := range content {
			if item.String("type") == "text" {
				messages = append(messages, item.String("text"))
			}
		}
		message := strings.Join(messages, "\n")
		if message == "" {
			message = "Computer Use failed"
		}
		return queryloop.ToolCallResult{}, errors.New(message)
	}
	data := []protocol.JSONObject{}
	number := 0
	for _, item := range content {
		if item.String("type") != "image" {
			text := ""
			if item.String("type") == "text" {
				text = item.String("text")
			}
			data = append(data, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("text")}, protocol.JSONField{Name: "text", Value: raw(text)}))
			continue
		}
		number++
		caption := computerUseCaption(item.String("data"), number, toolName == "zoom")
		if caption != "" {
			data = append(data, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("text")}, protocol.JSONField{Name: "text", Value: raw(caption)}))
		}
		mime := item.Get("mimeType")
		if mime == nil || string(mime) == "null" {
			mime = raw("image/jpeg")
		}
		source := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("base64")}, protocol.JSONField{Name: "media_type", Value: mime}, protocol.JSONField{Name: "data", Value: item.Get("data")})
		data = append(data, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("image")}, protocol.JSONField{Name: "source", Value: raw(source)}))
	}
	out := queryloop.ToolCallResult{Data: raw(data)}
	meta := protocol.NewJSONObject()
	for _, key := range []string{"_meta", "structuredContent"} {
		if value := original.Get(key); truthyMCPJSON(value) {
			meta.Set(key, value)
		}
	}
	if len(meta.OwnProperties()) > 0 {
		out.MCPMeta = raw(meta)
	}
	return out, nil
}

func (s *Service) computerUseServerStatus(ctx context.Context, scope *cooperative.Scope) (protocol.JSONObject, error) {
	future, e := native(s, scope, func() ([]protocol.JSONObject, error) {
		enabled, e := s.options.ComputerUseEnabled(ctx)
		if e != nil {
			return nil, e
		}
		if !enabled {
			return nil, nil
		}
		return s.computerUse.Tools(ctx)
	})
	if e != nil {
		return protocol.JSONObject{}, e
	}
	tools, e := future.Await(scope)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	status := "connected"
	if tools == nil {
		status = "disabled"
	}
	out := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(ComputerUseServerName)}, protocol.JSONField{Name: "status", Value: raw(status)})
	if status == "connected" {
		out.Set("serverInfo", raw(protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(ComputerUseServerName)}, protocol.JSONField{Name: "version", Value: raw("0.1.3")})))
	}
	if s.computerUseConfig.IsObject() {
		config := protocol.NewJSONObject()
		for _, key := range []string{"type", "command", "args"} {
			if value := s.computerUseConfig.Get(key); value != nil {
				config.Set(key, value)
			}
		}
		out.Set("config", raw(config))
		out.Set("scope", s.computerUseConfig.Get("scope"))
	}
	if status == "connected" {
		projected := []protocol.JSONObject{}
		for _, tool := range tools {
			flags := protocol.NewJSONObject()
			annotations := object(tool.Get("annotations"))
			for _, flag := range []struct{ source, target string }{{"readOnlyHint", "readOnly"}, {"destructiveHint", "destructive"}, {"openWorldHint", "openWorld"}} {
				if string(annotations.Get(flag.source)) == "true" {
					flags.Set(flag.target, raw(true))
				}
			}
			projected = append(projected, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: tool.Get("name")}, protocol.JSONField{Name: "annotations", Value: raw(flags)}))
		}
		out.Set("tools", raw(projected))
	}
	return out, nil
}
