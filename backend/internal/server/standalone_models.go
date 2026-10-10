package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// This is the original desktop's no-provider catalog, not an upstream catalog
// or an override of the Harness model selection.
func standaloneDefaultModels() []storage.Object {
	result := []storage.Object{}
	for _, row := range []struct{ id, name, description, window, effort string }{
		{"claude-fable-5-1", "Fable 5.1", "Highest capability for long-running tasks", "1m", "high"},
		{"claude-fable-5", "Fable 5", "Highest capability for long-running tasks", "1m", ""},
		{"claude-opus-5-5", "Opus 5.5", "Best for complex agentic coding and enterprise work", "1m", "medium"},
		{"claude-opus-5", "Opus 5", "Best for complex agentic coding and enterprise work", "1m", "high"},
		{"claude-opus-4-8", "Opus 4.8", "Best for complex agentic coding and enterprise work", "1m", "high"},
		{"claude-sonnet-5-5", "Sonnet 5.5", "Best combination of speed and intelligence", "1m", "medium"},
		{"claude-sonnet-5", "Sonnet 5", "Best combination of speed and intelligence", "1m", "high"},
		{"claude-haiku-4-5", "Haiku 4.5", "Fastest with near-frontier intelligence", "200k", ""},
	} {
		entry := storage.Object{"id": jsonString(row.id), "name": jsonString(row.name), "description": jsonString(row.description), "context": jsonString(row.window)}
		if row.effort != "" {
			entry["defaultReasoningEffort"] = jsonString(row.effort)
			entry["supportedReasoningEfforts"], _ = json.Marshal(providerconfig.EffortLevels())
		}
		result = append(result, entry)
	}
	return result
}

func (server *Server) standaloneModelsHTTP(writer http.ResponseWriter, request *http.Request, current bool) error {
	if server.options.Settings == nil {
		return unsupported("models standalone user settings")
	}
	// Retain explicit OAuth boundaries until their actual refresh/catalog
	// services are connected. A logged-in account must never silently switch.
	if err := server.checkStandaloneOAuthBoundary(); err != nil {
		return err
	}
	settings, err := server.options.Settings.GetUserSettings(request.Context())
	if err != nil {
		return err
	}
	var settingsEnv storage.Object
	_ = json.Unmarshal(settings["env"], &settingsEnv)
	env := map[string]string{}
	if server.options.Environment != nil {
		env = server.options.Environment()
	}
	resolve := func(key string) string {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
		return strings.TrimSpace(stringField(settingsEnv, key))
	}
	mapping := storage.Object{}
	for _, slot := range []string{"main", "haiku", "sonnet", "opus", "fable"} {
		key := "ANTHROPIC_DEFAULT_" + strings.ToUpper(slot) + "_MODEL"
		if slot == "main" {
			key = "ANTHROPIC_MODEL"
		}
		mapping[slot] = jsonString(resolve(key))
	}
	mappingJSON, _ := json.Marshal(mapping)
	models := providerconfig.ModelList(storage.Object{"models": mappingJSON})
	if len(models) == 0 {
		models = standaloneDefaultModels()
	}
	id := stringField(settings, "model")
	if id == "" {
		id = resolve("ANTHROPIC_MODEL")
	}
	if server.options.HahaOAuth != nil {
		configured := jsonString(id)
		if !current {
			configured = nil
		}
		official, err := server.options.HahaOAuth.ResolveRuntimeModel(request.Context(), configured)
		if err != nil {
			return err
		}
		if official != nil {
			models = standaloneDefaultModels()
			id = *official
		}
	}
	if !current {
		writeJSON(writer, 200, map[string]any{"models": models, "provider": nil})
		return nil
	}
	if id == "" {
		id = "claude-opus-5-5"
	}
	tier := stringField(settings, "modelContext")
	lookup := id
	if tier != "" {
		lookup += ":" + tier
	}
	var selected storage.Object
	for _, candidate := range []string{lookup, id} {
		for _, model := range models {
			if stringField(model, "id") == candidate {
				selected = model
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		window := tier
		if window == "" {
			window = "unknown"
		}
		selected = storage.Object{"id": jsonString(id), "name": jsonString(id), "description": jsonString("Custom model"), "context": jsonString(window)}
	}
	if tier != "" {
		selected["context"] = jsonString(tier)
	}
	writeJSON(writer, 200, map[string]any{"model": selected})
	return nil
}

func (server *Server) checkStandaloneOAuthBoundary() error {
	for _, name := range []string{"oauth.json", "openai-oauth.json", "grok-oauth.json"} {
		if name == "oauth.json" && server.options.HahaOAuth != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(server.options.ConfigDir, "cc-haha", name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			var token storage.Object
			if err = json.Unmarshal(data, &token); err != nil {
				return err
			}
			if stringField(token, "accessToken") != "" {
				return unsupported("models OAuth refresh/catalog resolution")
			}
		}
	}
	return nil
}
