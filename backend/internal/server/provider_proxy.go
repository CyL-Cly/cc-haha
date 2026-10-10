package server

import (
	"encoding/json"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
)

func (s *Server) proxyHTTP(w http.ResponseWriter, r *http.Request) {
	if s.options.ProviderProxy == nil {
		writeError(w, unsupported("provider proxy transport"))
		return
	}
	if err := s.options.ProviderProxy.Serve(w, r); err != nil {
		if s.options.Diagnostics != nil {
			details, _ := json.Marshal(map[string]any{"path": r.URL.Path, "method": r.Method, "error": err.Error()})
			_, _ = s.options.Diagnostics.Record(diagnosticsapi.Input{Type: "proxy_request_failed", Severity: "error", Summary: err.Error(), Details: details})
		}
		writeJSON(w, 500, map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": "Internal proxy error"}})
	}
}
