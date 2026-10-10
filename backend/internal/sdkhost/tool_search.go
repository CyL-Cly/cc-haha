package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

func (h *Host) captureToolSearchCatalogue(scope *cooperative.Scope) error {
	if h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Print.ToolSearch == nil {
		return nil
	}
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if len(h.definitions) != len(h.registered) {
		return cap("owning Host executable catalogue identity")
	}
	gate := toolsearch.GateInputs{Environment: h.options.OriginalEnv, ContextWindow: func(_ context.Context, _ string) (float64, error) { return h.InferenceContextWindow(scope) }}
	catalogue := &toolsearch.Catalogue{Gate: gate, Tools: []toolsearch.Tool{}, PendingServers: []json.RawMessage{}}
	for i, definition := range h.definitions {
		tool := h.registered[i]
		if tool.Name != definition.Name {
			return cap("owning Host executable catalogue order")
		}
		d := definition
		aliases := []json.RawMessage{}
		for _, alias := range tool.Aliases {
			aliases = append(aliases, quote(alias))
		}
		entry := toolsearch.Tool{Name: quote(tool.Name), Aliases: aliases, IsMCP: tool.IsMCP, AlwaysLoad: tool.AlwaysLoad, ShouldDefer: tool.ShouldDefer, SearchHint: tool.SearchHint}
		entry.Prompt = func(_ context.Context) (json.RawMessage, error) {
			host := toolschema.Host{MainLoopModel: h.askInferenceModel, BaseURL: h.options.OriginalEnv["ANTHROPIC_BASE_URL"], ToolSearchDeltaEnabled: h.options.OriginalEnv["USER_TYPE"] == "ant"}
			if h.options.Print.Tools != nil {
				actual, e := h.options.Print.Tools.SchemaHostForScope(scope, h.askInferenceModel, h.options.OriginalEnv["ANTHROPIC_BASE_URL"])
				if e != nil {
					return nil, e
				}
				host = *actual
				host.AgentDefinitions = nil
				host.ToolSearchDeltaEnabled = h.options.OriginalEnv["USER_TYPE"] == "ant"
			}
			return h.toolSchemas.OriginalPrompt(d, host)
		}
		entry.Schema = func(context.Context) (json.RawMessage, error) { return toolschema.OriginalInputSchema(d) }
		catalogue.Tools = append(catalogue.Tools, entry)
	}
	for _, peer := range h.mcpServers {
		if peer.String("status") == "pending" {
			catalogue.PendingServers = append(catalogue.PendingServers, peer.Get("name"))
		}
	}
	h.toolSearchCatalogue = catalogue
	return nil
}

// NativeToolSearchCatalogue captures the actual admitted pool on its owning
// scope and returns detached collections; callers cannot mutate Host state.
func (h *Host) NativeToolSearchCatalogue(scope *cooperative.Scope) (*toolsearch.Catalogue, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if e := h.captureToolSearchCatalogue(scope); e != nil {
		return nil, e
	}
	if h.toolSearchCatalogue == nil {
		return nil, nil
	}
	copy := *h.toolSearchCatalogue
	copy.Tools = append([]toolsearch.Tool{}, copy.Tools...)
	for i, tool := range copy.Tools {
		copy.Tools[i].Name = append(json.RawMessage(nil), tool.Name...)
		copy.Tools[i].SearchHint = append(json.RawMessage(nil), tool.SearchHint...)
		copy.Tools[i].Aliases = []json.RawMessage{}
		for _, alias := range tool.Aliases {
			copy.Tools[i].Aliases = append(copy.Tools[i].Aliases, append(json.RawMessage(nil), alias...))
		}
	}
	copy.PendingServers = []json.RawMessage{}
	for _, peer := range h.toolSearchCatalogue.PendingServers {
		copy.PendingServers = append(copy.PendingServers, append(json.RawMessage(nil), peer...))
	}
	copy.Gate.Environment = map[string]string{}
	for k, v := range h.toolSearchCatalogue.Gate.Environment {
		copy.Gate.Environment[k] = v
	}
	return &copy, nil
}
