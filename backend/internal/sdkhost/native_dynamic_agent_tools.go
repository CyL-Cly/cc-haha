package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func (h *Host) filterDynamicAgentTools(input requestcontext.Context) requestcontext.Context {
	if h.options.Print == nil || h.options.Print.ToolAllowedNow == nil {
		return input
	}
	allowed := h.options.Print.ToolAllowedNow
	tools := []protocol.JSONObject{}
	for _, tool := range input.Tools {
		if allowed(tool.String("name")) {
			tools = append(tools, tool)
		}
	}
	input.Tools = tools
	if input.Host.NativeToolDefinitions != nil {
		definitions := []toolschema.Definition{}
		for _, definition := range *input.Host.NativeToolDefinitions {
			if allowed(definition.Name) {
				definitions = append(definitions, definition)
			}
		}
		input.Host.NativeToolDefinitions = &definitions
	}
	if input.Host.ToolSearchCatalogue != nil {
		catalog := *input.Host.ToolSearchCatalogue
		catalog.Tools = nil
		for _, tool := range input.Host.ToolSearchCatalogue.Tools {
			var name string
			_ = json.Unmarshal(tool.Name, &name)
			if allowed(name) {
				catalog.Tools = append(catalog.Tools, tool)
			}
		}
		input.Host.ToolSearchCatalogue = &catalog
	}
	return input
}
