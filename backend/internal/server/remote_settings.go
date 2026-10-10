package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

var remoteCompatibilityKeys = []string{"maxOutputTokens", "outputTokenLimit", "outputTokenField", "sampling", "reasoning", "parallelTools", "structuredOutput"}
var remoteReservedProviderPaths = []string{"settings", "cc-switch", "test", "models", "presets", "auth-status", "official", "reorder"}

func remoteProviderAllowed(parts []string, method string) bool {
	if len(parts) == 2 {
		return method == "GET" || method == "POST"
	}
	id := parts[2]
	if len(parts) == 3 {
		if id == "presets" || id == "auth-status" {
			return method == "GET"
		}
		if id == "official" {
			return method == "POST"
		}
		if id == "reorder" {
			return method == "PUT"
		}
		return !slices.Contains(remoteReservedProviderPaths, id) && slices.Contains([]string{"GET", "PUT", "DELETE"}, method)
	}
	return len(parts) == 4 && !slices.Contains(remoteReservedProviderPaths, id) && parts[3] == "activate" && method == "POST"
}

func normalizedAutoQuestion(raw json.RawMessage) json.RawMessage {
	var value storage.Object
	_ = json.Unmarshal(raw, &value)
	enabled := string(bytes.TrimSpace(value["enabled"])) == "true"
	var timeout float64
	_ = json.Unmarshal(value["timeoutMinutes"], &timeout)
	if !slices.Contains([]float64{1, 5, 10, 30}, timeout) {
		timeout = 5
	}
	result, _ := json.Marshal(map[string]any{"enabled": enabled, "timeoutMinutes": timeout})
	return result
}

func projectRemoteSettings(settings storage.Object) storage.Object {
	result := storage.Object{}
	for _, key := range []string{"alwaysThinkingEnabled", "workflowKeywordTriggerEnabled", "autoDreamEnabled", "skipAutoPermissionPrompt", "chatSendBehavior", "outputStyle", "skipWebFetchPreflight", "language"} {
		var value any
		if raw, ok := settings[key]; ok && json.Unmarshal(raw, &value) == nil {
			switch value.(type) {
			case string, bool, float64:
				result[key] = raw
			}
		}
	}
	if value, ok := settings["autoQuestion"]; ok {
		result["autoQuestion"] = normalizedAutoQuestion(value)
	}
	return result
}

func validateRemoteSettings(input storage.Object) bool {
	for key, raw := range input {
		switch key {
		case "language":
			units, err := protocol.DecodeJavaScriptJSONString(raw)
			if err != nil || len(units) > 80 {
				return false
			}
		case "outputStyle":
			if !slices.Contains([]string{"default", "Explanatory", "Learning"}, stringField(input, key)) {
				return false
			}
		case "chatSendBehavior":
			if !slices.Contains([]string{"enter", "modifierEnter"}, stringField(input, key)) {
				return false
			}
		case "alwaysThinkingEnabled", "workflowKeywordTriggerEnabled":
			if value := string(bytes.TrimSpace(raw)); value != "true" && value != "false" {
				return false
			}
		case "autoQuestion":
			var value storage.Object
			if json.Unmarshal(raw, &value) != nil || len(value) != 2 {
				return false
			}
			if flag := string(bytes.TrimSpace(value["enabled"])); flag != "true" && flag != "false" {
				return false
			}
			var timeout float64
			if json.Unmarshal(value["timeoutMinutes"], &timeout) != nil || !slices.Contains([]float64{1, 5, 10, 30}, timeout) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func projectRemoteProvider(provider storage.Object) storage.Object {
	result := storage.Object{}
	for _, key := range []string{"id", "presetId", "name", "authStrategy", "baseUrl", "apiFormat", "runtimeKind", "models", "model1mSupport", "autoCompactWindow", "modelContextWindows", "toolSearchEnabled", "disableExperimentalBetas", "supportsNestedToolResultMedia", "notes"} {
		if raw, ok := provider[key]; ok {
			result[key] = raw
		}
	}
	result["apiKey"] = jsonString("")
	result["hasApiKey"], _ = json.Marshal(stringField(provider, "apiKey") != "")
	var compatibility storage.Object
	if json.Unmarshal(provider["requestCompatibility"], &compatibility) == nil && compatibility != nil {
		visible := storage.Object{}
		for _, key := range remoteCompatibilityKeys {
			if raw, ok := compatibility[key]; ok {
				visible[key] = raw
			}
		}
		result["requestCompatibility"], _ = json.Marshal(visible)
	}
	var image storage.Object
	if json.Unmarshal(provider["imageGeneration"], &image) == nil && image != nil {
		visible := storage.Object{"model": image["model"], "apiKey": jsonString("")}
		if raw, ok := image["baseUrl"]; ok {
			visible["baseUrl"] = raw
		}
		visible["hasApiKey"], _ = json.Marshal(stringField(image, "apiKey") != "")
		result["imageGeneration"], _ = json.Marshal(visible)
	}
	return result
}

func remoteCredentialRequired(current, input storage.Object) bool {
	next := stringField(current, "baseUrl")
	if _, err := protocol.DecodeJavaScriptJSONString(input["baseUrl"]); err == nil {
		next = stringField(input, "baseUrl")
	}
	replacesMain := strings.TrimSpace(stringField(input, "apiKey")) != ""
	if stringField(current, "apiKey") != "" && next != stringField(current, "baseUrl") && !replacesMain {
		return true
	}
	if string(bytes.TrimSpace(input["imageGeneration"])) == "null" {
		return false
	}
	var old, patch storage.Object
	_ = json.Unmarshal(current["imageGeneration"], &old)
	_ = json.Unmarshal(input["imageGeneration"], &patch)
	if old == nil && patch == nil {
		return false
	}
	oldDestination := strings.TrimSpace(stringField(old, "baseUrl"))
	if oldDestination == "" {
		oldDestination = stringField(current, "baseUrl")
	}
	nextDestination := strings.TrimSpace(stringField(old, "baseUrl"))
	if patch != nil {
		nextDestination = strings.TrimSpace(stringField(patch, "baseUrl"))
	}
	if nextDestination == "" {
		nextDestination = next
	}
	if nextDestination == oldDestination || patch != nil && strings.TrimSpace(stringField(patch, "apiKey")) != "" {
		return false
	}
	return strings.TrimSpace(stringField(old, "apiKey")) != "" || stringField(current, "apiKey") != "" && !replacesMain
}

type remoteJSONResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (response *remoteJSONResponse) Header() http.Header { return response.header }
func (response *remoteJSONResponse) WriteHeader(status int) {
	if response.status == 0 {
		response.status = status
	}
}
func (response *remoteJSONResponse) Write(data []byte) (int, error) {
	if response.status == 0 {
		response.status = 200
	}
	return response.body.Write(data)
}

// The original remote browser owns a field-level projection. It reuses the
// actual local handlers and settings transactions, including their errors.
func (server *Server) remoteSettingsHTTP(writer http.ResponseWriter, request *http.Request, parts []string) {
	provider := parts[1] == "providers"
	allowed := provider && remoteProviderAllowed(parts, request.Method) || !provider && len(parts) == 3 && parts[2] == "user" && slices.Contains([]string{"GET", "PUT"}, request.Method)
	if !allowed {
		writeJSON(writer, 403, map[string]string{"error": "Desktop-only capability"})
		return
	}
	fail := func() { writeJSON(writer, 400, map[string]string{"error": "Remote settings request failed"}) }
	create := provider && len(parts) == 2 && request.Method == "POST"
	if create || request.Method == "PUT" && (!provider || len(parts) == 3 && parts[2] != "reorder") {
		data, err := io.ReadAll(request.Body)
		if err != nil || !json.Valid(data) {
			fail()
			return
		}
		var input storage.Object
		if json.Unmarshal(data, &input) != nil || input == nil {
			writeJSON(writer, 400, map[string]string{"error": "Object required"})
			return
		}
		if !provider && !validateRemoteSettings(input) {
			writeJSON(writer, 400, map[string]string{"error": "Unsupported General setting"})
			return
		}
		if provider {
			var saved storage.Object
			if !create {
				service, err := providerconfig.New(providerconfig.Options{Store: server.options.Store, ConfigDir: server.options.ConfigDir})
				if err != nil {
					fail()
					return
				}
				saved, err = service.Provider(request.Context(), parts[2])
				if err != nil {
					fail()
					return
				}
				if remoteCredentialRequired(saved, input) {
					writeJSON(writer, 400, map[string]string{"error": "Changing a credential destination requires an explicit API key", "code": "REMOTE_PROVIDER_CREDENTIAL_REQUIRED"})
					return
				}
				if raw, ok := input["apiKey"]; ok && string(raw) == `""` {
					delete(input, "apiKey")
				}
			}
			if raw, ok := input["requestCompatibility"]; ok {
				var editable storage.Object
				if string(bytes.TrimSpace(raw)) == "null" || json.Unmarshal(raw, &editable) == nil && editable != nil {
					var current storage.Object
					_ = json.Unmarshal(saved["requestCompatibility"], &current)
					merged := storage.Object{}
					for key, raw := range current {
						if !slices.Contains(remoteCompatibilityKeys, key) {
							merged[key] = raw
						}
					}
					for key, raw := range editable {
						if slices.Contains(remoteCompatibilityKeys, key) {
							merged[key] = raw
						}
					}
					if len(merged) > 0 {
						input["requestCompatibility"], _ = json.Marshal(merged)
					} else if create {
						delete(input, "requestCompatibility")
					} else {
						input["requestCompatibility"] = json.RawMessage("null")
					}
				}
			}
			var image storage.Object
			if json.Unmarshal(input["imageGeneration"], &image) == nil && image != nil && !create {
				if raw, ok := image["apiKey"]; !ok || string(raw) == `""` {
					var current storage.Object
					_ = json.Unmarshal(saved["imageGeneration"], &current)
					if raw, ok := current["apiKey"]; ok {
						image["apiKey"] = raw
					} else {
						delete(image, "apiKey")
					}
				}
				input["imageGeneration"], _ = json.Marshal(image)
			}
		}
		data, err = json.Marshal(input)
		if err != nil {
			fail()
			return
		}
		request = request.Clone(request.Context())
		request.Body = io.NopCloser(bytes.NewReader(data))
		request.ContentLength = int64(len(data))
		request.Header.Set("Content-Length", strconv.Itoa(len(data)))
	}
	response := &remoteJSONResponse{header: http.Header{}}
	server.serveAPI(response, request, parts)
	if response.status >= 400 {
		for key, values := range response.header {
			writer.Header()[key] = values
		}
		writer.WriteHeader(response.status)
		_, _ = writer.Write(response.body.Bytes())
		return
	}
	var result storage.Object
	if json.Unmarshal(response.body.Bytes(), &result) != nil || result == nil {
		fail()
		return
	}
	if !provider && request.Method == "GET" {
		result = projectRemoteSettings(result)
	}
	if provider {
		var providers []storage.Object
		if json.Unmarshal(result["providers"], &providers) == nil && providers != nil {
			for i, value := range providers {
				providers[i] = projectRemoteProvider(value)
			}
			result["providers"], _ = json.Marshal(providers)
		}
		var value storage.Object
		if json.Unmarshal(result["provider"], &value) == nil && value != nil {
			result["provider"], _ = json.Marshal(projectRemoteProvider(value))
		}
	}
	writeJSON(writer, response.status, result)
}
