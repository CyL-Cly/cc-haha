package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// officialProviderModelsHTTP is the original /providers/:id/models route.
// The caller supplies the shared provider index reader, never a second index.
func (s *Server) officialProviderModelsHTTP(w http.ResponseWriter, r *http.Request, id string, provider *providerconfig.Service) error {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		return methodNotAllowed(r.Method)
	}
	var models desktopoauth.ModelMapping
	var err error
	if r.Method == http.MethodPut {
		models, err = parseOfficialModels(r)
		if err != nil {
			return err
		}
	}
	if !desktopoauth.BuiltInProvider(id) {
		return notFound("Official provider not found: " + id)
	}
	index, err := provider.ReadIndex(r.Context())
	if err != nil {
		return err
	}
	var mappings storage.Object
	_ = json.Unmarshal(index["officialProviderModels"], &mappings)
	if mappings == nil {
		mappings = storage.Object{}
	}
	if r.Method == http.MethodGet {
		if raw := mappings[id]; raw != nil && string(raw) != "null" {
			if err = json.Unmarshal(raw, &models); err != nil {
				return err
			}
			models = desktopoauth.NormalizeProviderModels(models)
		} else {
			models = desktopoauth.DefaultProviderModels(id)
		}
		writeJSON(w, 200, map[string]json.RawMessage{"models": desktopoauth.EncodeProviderModels(models)})
		return nil
	}
	models = desktopoauth.NormalizeProviderModels(models)
	mappings[id] = desktopoauth.EncodeProviderModels(models)
	index["officialProviderModels"], _ = json.Marshal(mappings)
	// The original provider service commits this index before syncing settings.
	// A later settings failure leaves the newly selected aliases on disk.
	if err = s.writeOfficialProviderIndex(index, time.Now().UnixMilli()); err != nil {
		return err
	}
	active := stringField(index, "activeId")
	if id == "claude-official" {
		if bytes.Equal(bytes.TrimSpace(index["activeId"]), []byte("null")) {
			if s.options.Settings == nil {
				return unsupported("providers/claude-official/models settings sync")
			}
			if err = s.options.Settings.UpdateOfficialModelMapping(r.Context(), models); err != nil {
				return err
			}
		}
	} else if active == id {
		if s.options.Managed == nil {
			return unsupported("providers/" + id + "/models managed settings sync")
		}
		var grokWindows *protocol.JSONObject
		if id == "grok-official" {
			grokWindows = s.grokRuntimeWindows()
		}
		env, err := desktopoauth.OfficialManagedEnvironment(id, s.options.ConfigDir, models, grokWindows)
		if err != nil {
			return err
		}
		_, err = settings.UpdateManaged(r.Context(), s.options.Managed, func(current storage.Object) (settings.ManagedUpdate[struct{}], error) {
			var existing storage.Object
			_ = json.Unmarshal(current["env"], &existing)
			if existing == nil {
				existing = storage.Object{}
			}
			for _, key := range providerconfig.ManagedKeys() {
				delete(existing, key)
			}
			var replacement storage.Object
			raw, _ := env.MarshalJSON()
			_ = json.Unmarshal(raw, &replacement)
			for key, value := range replacement {
				existing[key] = value
			}
			current["env"], _ = json.Marshal(existing)
			return settings.ManagedUpdate[struct{}]{Settings: current}, nil
		})
		if err != nil {
			return err
		}
		_, err = settings.UpdateManaged(r.Context(), s.options.Managed, func(current storage.Object) (settings.ManagedUpdate[struct{}], error) {
			current["model"], _ = json.Marshal(models.Main)
			delete(current, "modelContext")
			return settings.ManagedUpdate[struct{}]{Settings: current}, nil
		})
		if err != nil {
			return err
		}
	}
	writeJSON(w, 200, map[string]json.RawMessage{"models": desktopoauth.EncodeProviderModels(models)})
	return nil
}

func (s *Server) writeOfficialProviderIndex(index storage.Object, now int64) error {
	path := filepath.Join(s.options.ConfigDir, "cc-haha", "providers.json")
	fs := s.options.Store.FileSystem()
	if err := fs.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, now)
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(index); err != nil {
		return err
	}
	value := protocol.StringifyValidatedJavaScriptJSON(bytes.TrimSpace(buffer.Bytes()))
	var indented bytes.Buffer
	if err := json.Indent(&indented, value, "", "  "); err != nil {
		return err
	}
	err := fs.WriteFile(tmp, append(indented.Bytes(), '\n'), 0666)
	if err == nil {
		err = fs.Rename(tmp, path)
	}
	if err != nil {
		_ = fs.Remove(tmp)
		return &apiError{500, "INTERNAL_ERROR", "Failed to write providers index: " + storage.JavaScriptErrorText(err, storage.JavaScriptErrorContext{RenameSourceIsFile: true})}
	}
	return nil
}

// Zod object/string issue order is the same as ModelMappingSchema.extend.
func parseOfficialModels(r *http.Request) (desktopoauth.ModelMapping, error) {
	var result desktopoauth.ModelMapping
	raw, err := io.ReadAll(r.Body)
	if err != nil || !json.Valid(raw) {
		return result, badRequest("Invalid JSON body")
	}
	body, issue := schemaObject(raw)
	if issue != "" {
		return result, badRequest(issue)
	}
	models, issue := schemaObject(body["models"])
	if issue != "" {
		return result, badRequest(issue)
	}
	issues := []string{}
	for _, item := range []struct {
		name     string
		dest     *string
		optional bool
	}{{"main", &result.Main, false}, {"fable", &result.Fable, true}, {"haiku", &result.Haiku, false}, {"sonnet", &result.Sonnet, false}, {"opus", &result.Opus, false}} {
		value := models[item.name]
		if item.optional && value == nil {
			continue
		}
		if len(value) == 0 || bytes.TrimSpace(value)[0] != '"' || json.Unmarshal(value, item.dest) != nil {
			issues = append(issues, "Invalid input: expected string, received "+schemaType(value))
		} else if item.name == "main" && desktopoauth.NormalizeProviderModels(desktopoauth.ModelMapping{Main: *item.dest}).Main == "" {
			issues = append(issues, "Too small: expected string to have >=1 characters")
		}
	}
	if len(issues) > 0 {
		return result, badRequest(strings.Join(issues, "; "))
	}
	return result, nil
}

func schemaObject(raw json.RawMessage) (storage.Object, string) {
	if schemaType(raw) != "object" {
		return nil, "Invalid input: expected object, received " + schemaType(raw)
	}
	var value storage.Object
	_ = json.Unmarshal(raw, &value)
	return value, ""
}

func schemaType(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case 'n':
		return "null"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	default:
		return "number"
	}
}
