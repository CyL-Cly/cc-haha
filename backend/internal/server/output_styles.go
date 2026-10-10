package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type outputStyleItem struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

func trimJS(value string) string {
	return strings.TrimFunc(value, func(character rune) bool {
		return character >= '\t' && character <= '\r' || character == ' ' || character == '\u00a0' || character == '\u1680' ||
			character >= '\u2000' && character <= '\u200a' || character == '\u2028' || character == '\u2029' ||
			character == '\u202f' || character == '\u205f' || character == '\u3000' || character == '\ufeff'
	})
}

func (server *Server) outputStyleList(request *http.Request, workDir string) ([]outputStyleItem, error) {
	if server.options.OutputStyles == nil {
		return nil, unsupported("settings/output-styles native loader")
	}
	cwd := workDir
	if cwd == "" {
		cwd = server.options.WorkDir
	}
	styles, err := server.options.OutputStyles.List(request.Context(), cwd)
	if err != nil {
		return nil, err
	}
	items := make([]outputStyleItem, 0, len(styles))
	for _, style := range styles {
		item := outputStyleItem{Value: style.Name, Label: style.Name, Description: style.Description, Source: style.Source}
		if style.Nil {
			item.Label = "Default"
			item.Description = "Claude completes coding tasks efficiently and provides concise responses"
			item.Source = "built-in"
		}
		items = append(items, item)
	}
	return items, nil
}

func (server *Server) outputStylesHTTP(writer http.ResponseWriter, request *http.Request, sub string) error {
	if sub == "output-styles" {
		if request.Method != http.MethodGet {
			return methodNotAllowed(request.Method)
		}
		workDir := request.URL.Query().Get("workDir")
		if trimJS(workDir) == "" {
			workDir = ""
		}
		styles, err := server.outputStyleList(request, workDir)
		if err != nil {
			return err
		}
		current, err := server.options.Settings.GetUserSettings(request.Context())
		if err != nil {
			return err
		}
		scope := "userSettings"
		var path any
		if workDir != "" {
			path = workDir
			scope = "localSettings"
			for _, read := range []func() (storage.Object, error){func() (storage.Object, error) {
				return server.options.Settings.GetProjectSettings(request.Context(), workDir)
			}, func() (storage.Object, error) {
				return server.options.Settings.GetLocalSettings(request.Context(), workDir)
			}} {
				if overlay, err := read(); err == nil {
					for key, value := range overlay {
						current[key] = value
					}
				}
			}
		}
		style := "default"
		// Empty strings are still strings in Source's listing contract.
		_ = json.Unmarshal(current["outputStyle"], &style)
		writeJSON(writer, http.StatusOK, map[string]any{"outputStyle": style, "styles": styles, "scope": scope, "workDir": path})
		return nil
	}
	if request.Method != http.MethodPut {
		return methodNotAllowed(request.Method)
	}
	body, err := jsonObject(request)
	if err != nil {
		return err
	}
	style := stringField(body, "outputStyle")
	if trimJS(style) == "" {
		return badRequest(`Missing or invalid "outputStyle" in request body`)
	}
	workDir := stringField(body, "workDir")
	if trimJS(workDir) == "" {
		workDir = ""
	}
	styles, err := server.outputStyleList(request, workDir)
	if err != nil {
		return err
	}
	exists := false
	for _, item := range styles {
		exists = exists || item.Value == style
	}
	if !exists {
		return badRequest("Unknown output style: \"" + style + "\"")
	}
	serialized, _ := json.Marshal(style)
	patch := storage.Object{"outputStyle": serialized}
	scope := "userSettings"
	var path any
	if workDir != "" {
		path = workDir
		scope = "localSettings"
		err = server.options.Settings.UpdateLocalSettings(request.Context(), patch, workDir)
	} else {
		err = server.options.Settings.UpdateUserSettings(request.Context(), patch)
	}
	if err == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "outputStyle": style, "scope": scope, "workDir": path})
	}
	return err
}
