package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

func (m *MainPrint) nativeDeferredToolsAttachments(scope *cooperative.Scope, history []queryloop.Message, toolContexts ...*builtinwrite.CallContext) ([]queryloop.Message, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if m.Host == nil || m.toolSearch == nil {
		return []queryloop.Message{}, nil
	}
	var catalogue *toolsearch.Catalogue
	var e error
	var model string
	if len(toolContexts) > 0 && toolContexts[0] != nil {
		catalogue = toolContexts[0].ToolSearchCatalogue
		model = toolContexts[0].MainLoopModel
	} else {
		catalogue, e = m.Host.NativeToolSearchCatalogue(scope)
	}
	if e != nil {
		return nil, e
	}
	if catalogue == nil || !toolsearch.DeltaEnabled(catalogue.Gate) || !toolsearch.Optimistic(catalogue.Gate.Environment) {
		return []queryloop.Message{}, nil
	}
	if model == "" {
		model, e = m.Host.InferenceModel(scope)
	}
	if e != nil {
		return nil, e
	}
	supports, e := m.toolSearch.SupportsModel(model, catalogue.Gate)
	if e != nil {
		return nil, e
	}
	available := false
	for _, tool := range catalogue.Tools {
		available = available || toolsearch.ToolMatchesName(tool, toolsearch.Name)
	}
	if !supports || !available {
		return []queryloop.Message{}, nil
	}
	records := []protocol.JSONObject{}
	for _, message := range history {
		records = append(records, message.Record)
	}
	delta, e := toolsearch.DeferredToolsDelta(catalogue.Tools, records)
	if e != nil {
		return nil, e
	}
	if delta == nil {
		return []queryloop.Message{}, nil
	}
	data, e := json.Marshal(delta)
	if e != nil {
		return nil, e
	}
	var fields protocol.JSONObject
	if e := fields.UnmarshalJSON(data); e != nil {
		return nil, e
	}
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("deferred_tools_delta")})
	for _, field := range []string{"addedNames", "addedLines", "removedNames"} {
		payload.Set(field, fields.Get(field))
	}
	return []queryloop.Message{{Record: m.factory.CreateAttachment(rawMain(payload))}}, nil
}
