package server

import (
	"encoding/json"
	"net/http"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/ccswitch"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (server *Server) ccSwitchHTTP(writer http.ResponseWriter, request *http.Request, action string, service *providerconfig.Service) error {
	if action != "scan" && action != "import" {
		return notFound("Unknown cc-switch route: " + action)
	}
	expected := http.MethodGet
	if action == "import" {
		expected = http.MethodPost
	}
	if request.Method != expected {
		return methodNotAllowed(request.Method)
	}
	ids := []json.RawMessage{}
	if action == "import" {
		body, err := jsonObject(request)
		if err != nil {
			return err
		}
		if json.Unmarshal(body["sourceIds"], &ids) != nil || len(ids) == 0 {
			return badRequest("sourceIds must be a nonempty array of nonempty strings")
		}
		for _, id := range ids {
			u, e := protocol.DecodeJavaScriptJSONString(id)
			if e != nil || len(u) == 0 {
				return badRequest("sourceIds must be a nonempty array of nonempty strings")
			}
		}
	}
	index, err := service.ReadIndex(request.Context())
	if err != nil {
		return err
	}
	existing := []protocol.JSONObject{}
	_ = json.Unmarshal(index["providers"], &existing)
	env := map[string]string{}
	if server.options.Environment != nil {
		env = server.options.Environment()
	}
	scan, err := ccswitch.Read(request.Context(), ccswitch.Options{Home: server.options.HomeDir, Platform: runtime.GOOS, Environment: env, Existing: existing, Presets: providerconfig.Presets()})
	if err != nil {
		return err
	}
	if action == "scan" {
		writeJSON(writer, 200, scan.Public())
		return nil
	}
	inputs, skipped := scan.ResolveJSON(ids)
	saved := []storage.Object{}
	for _, input := range inputs {
		var value storage.Object
		data, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			return marshalErr
		}
		if err = json.Unmarshal(data, &value); err != nil {
			return err
		}
		saved = append(saved, value)
	}
	imported, err := service.ImportProviders(request.Context(), saved)
	if err != nil {
		return err
	}
	writeJSON(writer, 200, map[string]any{"imported": imported, "skipped": skipped})
	return nil
}
