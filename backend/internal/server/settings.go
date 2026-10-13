package server

import (
	"encoding/json"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (server *Server) settingsHTTP(writer http.ResponseWriter, request *http.Request, parts []string) error {
	if len(parts) > 2 && parts[1] == "settings" && parts[2] == "cli-launcher" {
		if request.Method != http.MethodGet {
			return methodNotAllowed(request.Method)
		}
		if server.options.CLILauncher == nil {
			return unsupported("settings/cli-launcher")
		}
		writeJSON(writer, 200, server.options.CLILauncher.Ensure(request.Context()))
		return nil
	}

	if server.options.Settings == nil {
		return unsupported("settings")
	}
	service := server.options.Settings
	sub := ""
	if len(parts) > 2 {
		sub = parts[2]
	}
	if parts[1] == "permissions" {
		if sub != "mode" {
			return notFound("Unknown permissions endpoint: " + sub)
		}
		switch request.Method {
		case http.MethodGet:
			mode, err := service.GetPermissionMode(request.Context())
			if err == nil {
				writeJSON(writer, 200, map[string]any{"mode": mode})
			}
			return err
		case http.MethodPut:
			body, err := jsonObject(request)
			if err != nil {
				return err
			}
			err = service.SetPermissionMode(request.Context(), stringField(body, "mode"))
			if err == nil {
				writeJSON(writer, 200, map[string]any{"ok": true, "mode": stringField(body, "mode")})
			}
			return err
		default:
			return methodNotAllowed(request.Method)
		}
	}
	if sub == "session-cleanup" {
		return server.sessionCleanupHTTP(writer, request)
	}
	if sub == "output-styles" || sub == "output-style" {
		return server.outputStylesHTTP(writer, request, sub)
	}
	if sub != "" && sub != "user" && sub != "project" {
		return unsupported("settings/" + sub)
	}
	if request.Method == http.MethodGet {
		var result storage.Object
		var err error
		switch sub {
		case "user":
			result, err = service.GetUserSettings(request.Context())
			if err == nil {
				enabled, e := service.GetAgentTeamsEnabled(request.Context())
				err = e
				if err == nil {
					result["agentTeamsEnabled"], _ = json.Marshal(enabled)
					result["autoUpdateEnabled"], _ = json.Marshal(settings.AutoUpdateEnabled(result))
					result["maxConcurrentSubagents"], _ = json.Marshal(settings.MaxConcurrentSubagents(result))
				}
			}
		case "project":
			result, err = service.GetProjectSettings(request.Context(), request.URL.Query().Get("projectRoot"))
		default:
			result, err = service.GetSettings(request.Context(), "")
		}
		if err == nil {
			writeJSON(writer, 200, result)
		}
		return err
	}
	if request.Method != http.MethodPut || sub == "" {
		return methodNotAllowed(request.Method)
	}
	body, err := jsonObject(request)
	if err != nil {
		return err
	}
	if sub == "user" {
		err = service.UpdateUserSettings(request.Context(), storage.Object(body))
	} else {
		err = service.UpdateProjectSettings(request.Context(), storage.Object(body), request.URL.Query().Get("projectRoot"))
	}
	if err == nil {
		if sub == "user" && body["autoQuestion"] != nil && server.options.AutoQuestion != nil {
			server.options.AutoQuestion.Refresh()
		}
		writeJSON(writer, 200, map[string]bool{"ok": true})
	}
	return err
}
