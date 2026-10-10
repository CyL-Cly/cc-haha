package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (server *Server) modelsHTTP(writer http.ResponseWriter, request *http.Request, parts []string) error {
	if parts[1] == "effort" {
		return server.effortHTTP(writer, request)
	}
	sub := ""
	if len(parts) > 2 {
		sub = parts[2]
	}
	if sub != "" && sub != "current" {
		return notFound("Unknown models endpoint: " + sub)
	}
	service, err := providerconfig.New(providerconfig.Options{Store: server.options.Store, ConfigDir: server.options.ConfigDir})
	if err != nil {
		return err
	}
	index, err := service.ReadIndex(request.Context())
	if err != nil {
		return err
	}
	activeID := stringField(index, "activeId")
	if sub == "current" && request.Method == http.MethodPut {
		body, err := jsonObject(request)
		if err != nil {
			return err
		}
		model := stringField(body, "modelId")
		if model == "" {
			return badRequest(`Missing or invalid "modelId" in request body`)
		}
		base, contextTier, composite := strings.Cut(model, ":")
		patch := storage.Object{"model": jsonString(base), "modelContext": nil}
		if composite && contextTier != "" {
			patch["modelContext"] = jsonString(contextTier)
		}
		if activeID != "" {
			if server.options.Managed == nil {
				return unsupported("models/current managed settings")
			}
			_, err = settings.UpdateManaged(request.Context(), server.options.Managed, func(current storage.Object) (settings.ManagedUpdate[struct{}], error) {
				for key, value := range patch {
					if value == nil {
						delete(current, key)
					} else {
						current[key] = value
					}
				}
				var env storage.Object
				_ = json.Unmarshal(current["env"], &env)
				if env == nil {
					env = storage.Object{}
				}
				attribution := "0"
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(base)), "claude") {
					attribution = "1"
				}
				env["CLAUDE_CODE_ATTRIBUTION_HEADER"] = jsonString(attribution)
				current["env"], _ = json.Marshal(env)
				return settings.ManagedUpdate[struct{}]{Settings: current}, nil
			})
		} else {
			if server.options.Settings == nil {
				return unsupported("models/current user settings")
			}
			err = server.options.Settings.UpdateUserSettings(request.Context(), patch)
		}
		if err == nil {
			writeJSON(writer, 200, map[string]any{"ok": true, "model": model})
		}
		return err
	}
	if request.Method != http.MethodGet {
		return methodNotAllowed(request.Method)
	}
	if activeID == "" {
		return server.standaloneModelsHTTP(writer, request, sub == "current")
	}
	provider, err := service.Provider(request.Context(), activeID)
	if err != nil {
		return err
	}
	models := providerconfig.ModelList(provider)
	if activeID == "openai-official" {
		if server.options.OpenAIModelCatalog == nil {
			return unsupported("models OpenAI catalog consumer")
		}
		catalog, err := server.options.OpenAIModelCatalog(request.Context())
		if err != nil {
			return err
		}
		models, err = openAICatalogAPIModels(catalog)
		if err != nil {
			return err
		}
	}
	if activeID == "grok-official" {
		if server.options.GrokModelCatalog == nil {
			return unsupported("models Grok catalog consumer")
		}
		catalog, err := server.options.GrokModelCatalog(request.Context())
		if err != nil {
			return err
		}
		models, err = grokCatalogAPIModels(catalog)
		if err != nil {
			return err
		}
	}
	if sub == "" {
		writeJSON(writer, 200, map[string]any{"models": models, "provider": map[string]string{"id": activeID, "name": stringField(provider, "name")}})
		return nil
	}
	if server.options.Managed == nil {
		return unsupported("models/current managed settings")
	}
	current, err := server.options.Managed.ReadSettings(request.Context())
	if err != nil {
		return err
	}
	var env, mapping storage.Object
	_ = json.Unmarshal(current["env"], &env)
	_ = json.Unmarshal(provider["models"], &mapping)
	id := stringField(current, "model")
	if id == "" {
		id = stringField(env, "ANTHROPIC_MODEL")
	}
	if id == "" {
		if activeID == "openai-official" {
			id = "gpt-6-sol"
		} else if activeID == "grok-official" {
			id = providerproxy.GrokDefaultMainModel
		} else {
			id = stringField(mapping, "main")
		}
	}
	contextTier := stringField(current, "modelContext")
	lookup := id
	if contextTier != "" {
		lookup = id + ":" + contextTier
	}
	var selected storage.Object
	for _, model := range models {
		if stringField(model, "id") == lookup {
			selected = model
			break
		}
	}
	if selected == nil {
		for _, model := range models {
			if stringField(model, "id") == id {
				selected = model
				break
			}
		}
	}
	if selected == nil {
		context := contextTier
		if context == "" {
			context = "unknown"
		}
		selected = storage.Object{"id": jsonString(id), "name": jsonString(id), "description": jsonString("Custom model"), "context": jsonString(context)}
	}
	if contextTier != "" {
		selected["context"] = jsonString(contextTier)
	}
	writeJSON(writer, 200, map[string]any{"model": selected})
	return nil
}
func jsonString(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }

// Remote catalog DTOs keep their original JSON units. Project those directly
// so a lone UTF-16 unit or a context outside a native integer cannot silently
// turn into a different model ID or window at the desktop API boundary.
func openAICatalogAPIModels(catalog []providerproxy.OpenAIModel) ([]storage.Object, error) {
	models := []storage.Object{}
	for _, entry := range catalog {
		contextWindow := ""
		if entry.ContextWindow != 0 {
			contextWindow = strconv.Itoa(entry.ContextWindow)
		}
		efforts, err := json.Marshal(entry.SupportedReasoningEfforts)
		if err != nil {
			return nil, err
		}
		model := storage.Object{"id": jsonString(entry.Value), "name": jsonString(entry.Label), "description": jsonString(entry.Description), "context": jsonString(contextWindow), "defaultReasoningEffort": jsonString(entry.DefaultReasoningEffort), "supportedReasoningEfforts": efforts}
		if len(entry.Raw) != 0 {
			var original protocol.JSONObject
			if err = original.UnmarshalJSON(entry.Raw); err != nil {
				return nil, err
			}
			for destination, source := range map[string]string{"id": "value", "name": "label", "description": "description", "defaultReasoningEffort": "defaultReasoningEffort", "supportedReasoningEfforts": "supportedReasoningEfforts"} {
				model[destination] = original.Get(source)
			}
			model["context"] = jsonString("")
			if sourceJSONTruthy(original.Get("contextWindow")) {
				model["context"], err = protocol.ExactJavaScriptStringJSON(original.Get("contextWindow"))
				if err != nil {
					return nil, err
				}
			}
		}
		models = append(models, model)
	}
	return models, nil
}
func (server *Server) effortHTTP(writer http.ResponseWriter, request *http.Request) error {
	if server.options.Settings == nil {
		return unsupported("effort user settings")
	}
	levels := providerconfig.EffortLevels()
	switch request.Method {
	case http.MethodGet:
		current, err := server.options.Settings.GetUserSettings(request.Context())
		if err != nil {
			return err
		}
		level := stringField(current, "effort")
		if !slices.Contains(levels, level) {
			level = "max"
		}
		writeJSON(writer, 200, map[string]any{"level": level, "available": levels})
		return nil
	case http.MethodPut:
		body, err := jsonObject(request)
		if err != nil {
			return err
		}
		var level string
		if json.Unmarshal(body["level"], &level) != nil || string(body["level"]) == "null" {
			return badRequest(`Missing or invalid "level" in request body`)
		}
		if !slices.Contains(levels, level) {
			return badRequest(fmt.Sprintf("Invalid effort level: %q. Valid levels: %s", level, strings.Join(levels, ", ")))
		}
		if err = server.options.Settings.UpdateUserSettings(request.Context(), storage.Object{"effort": jsonString(level)}); err == nil {
			writeJSON(writer, 200, map[string]any{"ok": true, "level": level})
		}
		return err
	default:
		return methodNotAllowed(request.Method)
	}
}
