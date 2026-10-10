package server

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func grokCatalogAPIModels(catalog []providerproxy.GrokModel) ([]storage.Object, error) {
	models := []storage.Object{}
	for _, entry := range catalog {
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		var original protocol.JSONObject
		if err = original.UnmarshalJSON(raw); err != nil {
			return nil, err
		}
		model := storage.Object{"id": original.Get("value"), "name": original.Get("label"), "description": original.Get("description"), "context": jsonString("")}
		if sourceJSONTruthy(original.Get("contextWindow")) {
			model["context"], err = protocol.ExactJavaScriptStringJSON(original.Get("contextWindow"))
			if err != nil {
				return nil, err
			}
		}
		if sourceJSONTruthy(original.Get("reasoningEffort")) {
			model["defaultReasoningEffort"] = original.Get("reasoningEffort")
		}
		if string(original.Get("supportsReasoningEffort")) == "false" {
			model["supportedReasoningEfforts"] = json.RawMessage("[]")
		} else if sourceJSONTruthy(original.Get("reasoningEfforts")) {
			model["supportedReasoningEfforts"] = original.Get("reasoningEfforts")
		}
		models = append(models, model)
	}
	return models, nil
}

func (s *Server) grokRuntimeWindows() *protocol.JSONObject {
	windows := protocol.NewJSONObject()
	if s.options.GrokRuntimeModelCatalog == nil {
		return &windows
	}
	for _, model := range s.options.GrokRuntimeModelCatalog() {
		if model.ContextWindow <= 0 {
			continue
		}
		key := jsonString(model.Value)
		value, _ := json.Marshal(model.ContextWindow)
		if len(model.Raw) > 0 {
			var raw protocol.JSONObject
			_ = raw.UnmarshalJSON(model.Raw)
			key = raw.Get("value")
			value = raw.Get("contextWindow")
		}
		_ = windows.SetPropertyJSON(key, value)
	}
	return &windows
}
