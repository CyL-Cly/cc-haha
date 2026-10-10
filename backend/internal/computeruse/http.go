package computeruse

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
func (service *Service) HandleHTTP(writer http.ResponseWriter, request *http.Request, parts []string) {
	action := "undefined"
	if len(parts) > 2 {
		action = parts[2]
	}
	ctx := request.Context()
	switch {
	case action == "status" && request.Method == http.MethodGet:
		status, err := service.Status(ctx)
		if err != nil {
			writeJSON(writer, 501, map[string]string{"error": "GO_MIGRATION_NOT_READY", "message": err.Error()})
			return
		}
		writeJSON(writer, 200, status)
	case action == "authorized-apps" && request.Method == http.MethodGet:
		config, err := service.Config(ctx)
		if err != nil {
			writeJSON(writer, 500, invalidConfigResponse(service, err, false))
			return
		}
		writeJSON(writer, 200, config)
	case action == "authorized-apps" && request.Method == http.MethodPut:
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			raw = nil
		}
		err = service.UpdateConfig(ctx, raw)
		var configured *ConfigError
		if errors.As(err, &configured) && configured.InvalidPatch {
			writeJSON(writer, 400, map[string]string{"error": "INVALID_COMPUTER_USE_CONFIG", "message": "Invalid Computer Use config"})
			return
		}
		if errors.As(err, &configured) && configured.Existing {
			writeJSON(writer, 409, invalidConfigResponse(service, configured.Err, true))
			return
		}
		if err != nil {
			writeJSON(writer, 500, map[string]string{"error": "COMPUTER_USE_CONFIG_WRITE_FAILED", "message": "Could not save Computer Use config"})
			return
		}
		writeJSON(writer, 200, map[string]bool{"ok": true})
	case action == "setup" && request.Method == http.MethodPost:
		if service.options.Platform == "darwin" {
			writeJSON(writer, 200, map[string]any{"success": false, "steps": []any{map[string]any{"name": "native_runtime", "ok": false, "message": "Computer Use on macOS uses the built-in native runtime and does not require Python setup."}}})
			return
		}
		if service.options.Platform == "win32" {
			result, err := service.WindowsSetup(ctx)
			if err != nil {
				writeJSON(writer, 500, map[string]string{"error": "INTERNAL_ERROR", "message": "An unexpected error occurred"})
				return
			}
			writeJSON(writer, 200, result)
			return
		}
		writeJSON(writer, 200, map[string]any{"success": false, "steps": []any{map[string]any{"name": "platform", "ok": false, "message": "Computer Use does not support platform: " + service.options.Platform}}})
	case action == "apps" && request.Method == http.MethodGet:
		if service.options.Platform == "win32" {
			value := map[string]any{}
			if apps := service.windowsInstalledApps(ctx); apps != nil {
				value["apps"] = apps
			}
			writeJSON(writer, 200, value)
			return
		}
		apps, err := service.InstalledApps(ctx)
		if err != nil {
			writeJSON(writer, 501, map[string]string{"error": "GO_MIGRATION_NOT_READY", "message": err.Error()})
			return
		}
		writeJSON(writer, 200, map[string]any{"apps": apps})
	case action == "app-icon" && request.Method == http.MethodGet:
		if service.options.Platform != "darwin" {
			http.Error(writer, "Not found", 404)
			return
		}
		id := trim(request.URL.Query().Get("bundleId"))
		if id == "" {
			writeJSON(writer, 400, map[string]string{"error": "bundleId is required"})
			return
		}
		png := service.AppIcon(ctx, id, request.URL.Query().Get("size"))
		if png == nil {
			writer.WriteHeader(404)
			_, _ = writer.Write([]byte("Not found"))
			return
		}
		writer.Header().Set("Content-Type", "image/png")
		writer.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = writer.Write(png)
	case (action == "permission-card" || action == "open-permission-card") && request.Method == http.MethodPost:
		writeJSON(writer, 200, service.PermissionCard(ctx))
	case action == "request-access" && request.Method == http.MethodPost:
		writeJSON(writer, 410, map[string]string{"error": "APP_AUTHORIZATION_REMOVED", "message": "Per-application Computer Use authorization is no longer required."})
	case action == "open-settings" && request.Method == http.MethodPost:
		raw, err := io.ReadAll(request.Body)
		body, ok := decodeObject(raw)
		if err != nil || !ok {
			writeJSON(writer, 400, map[string]string{"error": "INVALID_OPEN_SETTINGS_REQUEST", "message": "Invalid settings request"})
			return
		}
		pane := "Privacy_ScreenCapture"
		if raw, found := body["pane"]; found && string(raw) != "null" {
			pane, ok = stringValue(raw)
		}
		if !ok || (pane != "Privacy_ScreenCapture" && pane != "Privacy_Accessibility") {
			writeJSON(writer, 400, map[string]string{"error": "Invalid pane"})
			return
		}
		command, args := "open", []string{"x-apple.systempreferences:com.apple.preference.security?" + pane}
		if service.options.Platform == "win32" {
			command, args = "cmd", []string{"/c", "start", "ms-settings:privacy"}
		} else if service.options.Platform != "darwin" {
			writeJSON(writer, 400, map[string]string{"error": "UNSUPPORTED_PLATFORM", "message": "Computer Use is not supported on this platform"})
			return
		}
		result, err := service.run(ctx, command, args, nil)
		if err != nil || result.Code != 0 {
			writeJSON(writer, 500, map[string]string{"error": "OPEN_SETTINGS_FAILED", "message": "Could not open system settings"})
			return
		}
		writeJSON(writer, 200, map[string]bool{"ok": true})
	default:
		writeJSON(writer, 404, map[string]string{"error": "NOT_FOUND", "message": "Unknown computer-use action: " + action})
	}
}
