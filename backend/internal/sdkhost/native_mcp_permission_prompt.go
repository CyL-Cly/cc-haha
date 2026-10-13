package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"strings"
)

type nativePermissionPromptConfigurationError struct{ message string }

func (e *nativePermissionPromptConfigurationError) Error() string                { return e.message }
func (e *nativePermissionPromptConfigurationError) FatalToolConfiguration() bool { return true }
func nativePermissionNameMatches(tool queryloop.RegisteredTool, name string) bool {
	if tool.Name == name {
		return true
	}
	for _, alias := range tool.Aliases {
		if alias == name {
			return true
		}
	}
	return false
}
func (h *Host) resolveNativeMCPPermissionPrompt(scope *cooperative.Scope) (*queryloop.RegisteredTool, error) {
	if h.options.Print == nil || h.options.Print.PermissionPromptToolName == "" || h.options.Print.PermissionPromptToolName == "stdio" {
		return nil, nil
	}
	if !scope.BelongsTo(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if h.nativePermissionPromptTool != nil {
		return h.nativePermissionPromptTool, nil
	}
	names := []string{}
	if h.options.Print.NativeMCP != nil {
		_, tools, err := h.options.Print.NativeMCP.PermissionPromptCatalog(scope)
		if err != nil {
			return nil, err
		}
		for _, tool := range tools {
			names = append(names, tool.Name)
			if nativePermissionNameMatches(tool, h.options.Print.PermissionPromptToolName) {
				selected := tool
				h.nativePermissionPromptTool = &selected
				return &selected, nil
			}
		}
	}
	available := strings.Join(names, ", ")
	if available == "" {
		available = "none"
	}
	return nil, &nativePermissionPromptConfigurationError{fmt.Sprintf("Error: MCP tool %s (passed via --permission-prompt-tool) not found. Available MCP tools: %s", h.options.Print.PermissionPromptToolName, available)}
}
func nativeMCPPermissionAbort(tool queryloop.RegisteredTool, in *protocol.JSONObject) queryloop.ToolPermission {
	return queryloop.ToolPermission{Behavior: "deny", Input: in, Message: quote("Permission prompt was aborted."), DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("permissionPromptTool")}, protocol.JSONField{Name: "permissionPromptToolName", Value: quote(tool.Name)}))}
}
func (h *Host) callNativeMCPPermissionPrompt(ctx context.Context, scope *cooperative.Scope, prompt, protected queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
	if !scope.BelongsTo(h.options.Owner) || !prompt.IsMCP || prompt.CallJSON == nil {
		return queryloop.ToolPermission{}, cooperative.ErrRestricted
	}
	if ctx.Err() != nil {
		return nativeMCPPermissionAbort(protected, in), nil
	}
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "tool_name", Value: quote(protected.Name)}, protocol.JSONField{Name: "input", Value: raw(in)}, protocol.JSONField{Name: "tool_use_id", Value: call.ToolUseID})
	result, err := prompt.CallJSON(ctx, payload, call)
	if ctx.Err() != nil {
		return nativeMCPPermissionAbort(protected, in), nil
	}
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	mapped, err := prompt.MapResultJSON(result.Data, quote("1"))
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	var content []protocol.JSONObject
	if err = json.Unmarshal(mapped.Get("content"), &content); err != nil || len(content) == 0 || content[0].String("type") != "text" || !bytes.HasPrefix(bytes.TrimSpace(content[0].Get("text")), []byte(`"`)) {
		return queryloop.ToolPermission{}, errors.New("Permission prompt tool returned an invalid result. Expected a single text block param with type=\"text\" and a string text value.")
	}
	var text string
	if err = json.Unmarshal(content[0].Get("text"), &text); err != nil {
		return queryloop.ToolPermission{}, err
	}
	parsed, err := sdkPermissionOutput(json.RawMessage(text))
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	callback := NativeSDKPermissionPrompt{host: h, tool: prompt, input: in, call: call}
	return callback.Complete(scope, parsed, nil)
}
