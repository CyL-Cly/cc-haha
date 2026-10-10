package server

import (
	"encoding/json"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (server *Server) providersHTTP(writer http.ResponseWriter, request *http.Request, parts []string) error {
	if len(parts) > 2 && parts[2] == "test" && request.Method == http.MethodPost {
		return server.providerConnectionTestHTTP(writer, request, "", server.options.ProviderTest)
	}
	if len(parts) > 2 && parts[2] == "models" && request.Method == http.MethodPost {
		return server.providerModelsHTTP(writer, request, server.options.ProviderModels)
	}
	service, err := providerconfig.New(providerconfig.Options{Store: server.options.Store, ConfigDir: server.options.ConfigDir, Managed: server.options.Managed, Settings: server.options.Settings, OpenAIModelCatalog: server.options.OpenAIModelCatalog, GrokModelCatalog: server.options.GrokModelCatalog, GrokRuntimeModelCatalog: server.options.GrokRuntimeModelCatalog, ServerPort: server.options.ProviderProxyPort, ProxyReady: server.options.ProviderProxy != nil})
	if err != nil {
		return err
	}
	id, action := "", ""
	if len(parts) > 2 {
		id = parts[2]
	}
	if len(parts) > 3 {
		action = parts[3]
	}
	ctx := request.Context()
	switch id {
	case "settings":
		return server.managedSettingsHTTP(writer, request)
	case "presets":
		if request.Method != http.MethodGet {
			return methodNotAllowed(request.Method)
		}
		writeJSON(writer, 200, map[string]json.RawMessage{"presets": providerconfig.Presets()})
		return nil
	case "auth-status":
		if request.Method != http.MethodGet {
			return methodNotAllowed(request.Method)
		}
		index, err := service.ReadIndex(ctx)
		if err != nil {
			return err
		}
		activeID := stringField(index, "activeId")
		if activeID == "openai-official" {
			if server.options.HahaOpenAIOAuth == nil {
				return unsupported("providers/auth-status OpenAI OAuth service")
			}
			tokens, err := server.options.HahaOpenAIOAuth.EnsureFreshTokens(ctx)
			if err != nil {
				return err
			}
			hasAuth := tokens != nil && sourceJSONTruthy(tokens.Record().Get("accessToken")) && sourceJSONTruthy(tokens.Record().Get("refreshToken"))
			source := "none"
			if hasAuth {
				source = "openai-oauth"
			}
			writeJSON(writer, 200, map[string]any{"hasAuth": hasAuth, "source": source, "activeProvider": "ChatGPT Official"})
			return nil
		}
		if activeID == "grok-official" {
			if server.options.HahaGrokOAuth == nil {
				return unsupported("providers/auth-status Grok OAuth service")
			}
			tokens, err := server.options.HahaGrokOAuth.EnsureFreshTokens(ctx)
			if err != nil {
				return err
			}
			hasAuth := tokens != nil && sourceJSONTruthy(tokens.Record().Get("accessToken")) && sourceJSONTruthy(tokens.Record().Get("refreshToken"))
			source := "none"
			if hasAuth {
				source = "grok-oauth"
			}
			writeJSON(writer, 200, map[string]any{"hasAuth": hasAuth, "source": source, "activeProvider": "Grok Official"})
			return nil
		}
		if activeID != "" {
			provider, err := service.Provider(ctx, activeID)
			if err != nil {
				return err
			}
			env, err := providerconfig.BuildManagedEnv(provider, providerconfig.ManagedEnvironmentOptions{ServerPort: server.options.ProviderProxyPort, ProxyReady: server.options.ProviderProxy != nil})
			if err != nil {
				return err
			}
			if env["ANTHROPIC_API_KEY"] != "" || env["ANTHROPIC_AUTH_TOKEN"] != "" {
				writeJSON(writer, 200, map[string]any{"hasAuth": true, "source": "cc-haha-provider", "activeProvider": stringField(provider, "name")})
				return nil
			}
		} else if err := server.checkStandaloneOAuthBoundary(); err != nil {
			return err
		}
		if activeID == "" && server.options.HahaOAuth != nil {
			tokens, err := server.options.HahaOAuth.EnsureFreshTokens(ctx)
			if err != nil {
				return err
			}
			if tokens != nil && tokens.Record().String("accessToken") != "" {
				writeJSON(writer, 200, map[string]any{"hasAuth": true, "source": "claude-oauth", "activeProvider": "Claude Official"})
				return nil
			}
		}
		if server.options.Environment != nil {
			env := server.options.Environment()
			if env["ANTHROPIC_API_KEY"] != "" || env["ANTHROPIC_AUTH_TOKEN"] != "" {
				writeJSON(writer, 200, map[string]any{"hasAuth": true, "source": "env"})
				return nil
			}
		}
		if server.options.Settings == nil {
			return unsupported("providers/auth-status original settings")
		}
		original, err := server.options.Settings.GetUserSettings(ctx)
		if err != nil {
			writeJSON(writer, 200, map[string]any{"hasAuth": false, "source": "none"})
			return nil
		}
		var originalEnv map[string]json.RawMessage
		_ = json.Unmarshal(original["env"], &originalEnv)
		if stringField(originalEnv, "ANTHROPIC_API_KEY") != "" || stringField(originalEnv, "ANTHROPIC_AUTH_TOKEN") != "" {
			writeJSON(writer, 200, map[string]any{"hasAuth": true, "source": "original-settings"})
			return nil
		}
		writeJSON(writer, 200, map[string]any{"hasAuth": false, "source": "none"})
		return nil
	case "reorder":
		if request.Method != http.MethodPut {
			return methodNotAllowed(request.Method)
		}
		body, err := jsonObject(request)
		if err != nil {
			return err
		}
		var ids []string
		if json.Unmarshal(body["orderedIds"], &ids) != nil || len(ids) == 0 {
			return badRequest("orderedIds must be a nonempty array of nonempty strings")
		}
		for _, id := range ids {
			if id == "" {
				return badRequest("orderedIds must be a nonempty array of nonempty strings")
			}
		}
		result, err := service.Reorder(ctx, ids)
		if err == nil {
			writeJSON(writer, 200, result)
		}
		return err
	case "official":
		if request.Method != http.MethodPost {
			return methodNotAllowed(request.Method)
		}
		if err = service.ActivateOfficial(ctx); err == nil {
			writeJSON(writer, 200, map[string]bool{"ok": true})
		}
		return err
	case "cc-switch":
		return server.ccSwitchHTTP(writer, request, action, service)
	case "test", "models":
		return unsupported("providers/" + id + "/" + action)
	case "":
		switch request.Method {
		case http.MethodGet:
			index, err := service.ReadIndex(ctx)
			if err == nil {
				writeJSON(writer, 200, map[string]json.RawMessage{"providers": index["providers"], "activeId": index["activeId"]})
			}
			return err
		case http.MethodPost:
			body, err := jsonObject(request)
			if err != nil {
				return err
			}
			provider, err := service.Add(ctx, storage.Object(body))
			if err == nil {
				writeJSON(writer, 201, map[string]any{"provider": provider})
			}
			return err
		default:
			return methodNotAllowed(request.Method)
		}
	}
	if action != "" {
		switch action {
		case "activate":
			if request.Method != http.MethodPost {
				return methodNotAllowed(request.Method)
			}
			if err = service.Activate(ctx, id); err == nil {
				writeJSON(writer, 200, map[string]bool{"ok": true})
			}
			return err
		case "models":
			return server.officialProviderModelsHTTP(writer, request, id, service)
		case "test":
			return server.providerConnectionTestHTTP(writer, request, id, server.options.ProviderTest)
		default:
			return notFound("Unknown provider action: " + action)
		}
	}
	switch request.Method {
	case http.MethodGet:
		provider, err := service.Provider(ctx, id)
		if err == nil {
			writeJSON(writer, 200, map[string]any{"provider": provider})
		}
		return err
	case http.MethodPut:
		body, err := jsonObject(request)
		if err != nil {
			return err
		}
		provider, err := service.Update(ctx, id, storage.Object(body))
		if err == nil {
			writeJSON(writer, 200, map[string]any{"provider": provider})
		}
		return err
	case http.MethodDelete:
		err := service.Delete(ctx, id)
		if err == nil {
			writeJSON(writer, 200, map[string]bool{"ok": true})
		}
		return err
	default:
		return methodNotAllowed(request.Method)
	}
}

func (server *Server) managedSettingsHTTP(writer http.ResponseWriter, request *http.Request) error {
	if server.options.Managed == nil {
		return unsupported("providers/settings")
	}
	switch request.Method {
	case http.MethodGet:
		result, err := server.options.Managed.ReadSettings(request.Context())
		if err == nil {
			writeJSON(writer, 200, result)
		}
		return err
	case http.MethodPut:
		body, err := jsonObject(request)
		if err != nil {
			return err
		}
		_, err = settings.UpdateManaged(request.Context(), server.options.Managed, func(current storage.Object) (settings.ManagedUpdate[struct{}], error) {
			for key, value := range body {
				current[key] = value
			}
			return settings.ManagedUpdate[struct{}]{Settings: current}, nil
		})
		if err == nil {
			writeJSON(writer, 200, map[string]bool{"ok": true})
		}
		return err
	default:
		return methodNotAllowed(request.Method)
	}
}
