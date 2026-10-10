package server

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/petaccess"
)

type remoteBrowserKey struct{}

func (server *Server) authorize(writer http.ResponseWriter, request *http.Request) bool {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	href := scheme + "://" + request.Host + request.URL.RequestURI()
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	var override *string
	if value, ok := request.URL.Query()["localToken"]; ok && len(value) > 0 {
		override = &value[0]
	} else if value, ok := request.URL.Query()["token"]; ok && len(value) > 0 {
		override = &value[0]
	}
	auth := server.options.Authorization
	var petOverride *string
	if strings.HasPrefix(request.URL.Path, "/ws/") {
		if values, ok := request.URL.Query()["token"]; ok && len(values) > 0 {
			petOverride = &values[0]
		}
	}
	pet := auth.PetAuthorized(request.Header, petOverride)
	if pet {
		if !petaccess.HTTPAllowed(request.Method, request.URL.EscapedPath(), request.Header.Get("Access-Control-Request-Method")) {
			writeJSON(writer, 403, map[string]string{"error": "Forbidden", "message": "The pet token cannot access this capability."})
			return false
		}
		if id := petaccess.ScopedSessionID(request.URL.EscapedPath()); id != "" {
			allowed, e := server.petSessionAllowed(request.Context(), id)
			if e != nil {
				writeError(writer, e)
				return false
			}
			if !allowed {
				writeJSON(writer, 403, map[string]string{"error": "Forbidden", "message": "The pet token cannot access this session."})
				return false
			}
		}
		*request = *request.WithContext(context.WithValue(request.Context(), petAccessKey{}, true))
	}
	policy := access.Request{Method: request.Method, URL: href, Headers: request.Header}
	policyContext := access.H5Context{ClientAddress: host, TrustedRendererOrigin: access.ResolveTrustedRendererOrigin(server.options.TrustedRendererOrigin), LocalAccessTokenConfigured: auth.HasLocalToken() || auth.HasPetToken(), LocalAccessAuthorized: auth.LocalAuthorized(request.Header, override) || pet}
	if strings.HasPrefix(request.URL.Path, "/sdk/") {
		id := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		policyContext.InternalSDKAuthorized = server.authorizeSessionCredential(id, request.URL.Query().Get("token"))
	}
	enabled := false
	if server.options.H5 != nil {
		settings, err := server.options.H5.GetSettings(request.Context())
		if err != nil {
			writeError(writer, err)
			return false
		}
		enabled = settings.Enabled
	}
	corsOptions := access.CORSOptions{H5Enabled: enabled}
	if server.options.H5 != nil {
		corsOptions.IsOriginAllowed = server.options.H5.IsOriginAllowed
	}
	cors, err := access.ResolveCORS(request.Context(), request.Header.Get("Origin"), corsOptions)
	if err != nil {
		writeError(writer, err)
		return false
	}
	for name, value := range cors.Headers {
		writer.Header().Set(name, value)
	}
	kind, err := access.ClassifyH5Request(policy, policyContext)
	if err != nil {
		writeError(writer, badRequest("Invalid request URL"))
		return false
	}
	if kind == access.RequestH5Browser {
		*request = *request.WithContext(context.WithValue(request.Context(), remoteBrowserKey{}, true))
	}
	control := access.IsH5AccessControlPath(request.URL.Path) || access.IsLocalCredentialOnlyPath(request.URL.Path)
	preflight := access.IsH5AccessControlPath(request.URL.Path) && request.Method == http.MethodOptions && policyContext.TrustedRendererOrigin != "" && request.Header.Get("Origin") == policyContext.TrustedRendererOrigin && request.Header.Get("Access-Control-Request-Method") != "" && kind == access.RequestLocalTrusted
	if control && !preflight && (access.RequiresLocalAccessCredential(request.URL.Path, policyContext) || kind != access.RequestLocalTrusted) {
		message := "H5 access settings can only be changed from the local desktop app."
		if access.IsLocalCredentialOnlyPath(request.URL.Path) {
			message = "This action can only be performed from the local desktop app."
		}
		writeJSON(writer, 403, map[string]string{"error": "Forbidden", "message": message})
		return false
	}
	blocked, err := access.ShouldBlockDisabledH5Access(policy, policyContext, enabled, server.options.AuthRequired)
	if err != nil {
		writeError(writer, err)
		return false
	}
	if blocked {
		writeJSON(writer, 403, map[string]string{"error": "Forbidden", "message": "H5 access is disabled. Enable H5 access from the local desktop app first."})
		return false
	}
	if request.Method != http.MethodOptions && strings.HasPrefix(request.URL.Path, "/sdk/") && kind != access.RequestInternalSDK {
		writeJSON(writer, 403, map[string]string{"error": "Forbidden", "message": "H5 access settings can only be changed from the local desktop app."})
		return false
	}
	if cors.Rejected {
		writeJSON(writer, 403, map[string]string{"error": "CORS origin not allowed"})
		return false
	}
	if request.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusNoContent)
		return false
	}
	requireH5, err := access.ShouldRequireH5Token(policy, policyContext, enabled)
	if err != nil {
		writeError(writer, err)
		return false
	}
	if !pet && (requireH5 || server.options.AuthRequired) {
		result, err := auth.ValidateRequest(request.Context(), request.Header, override)
		if requireH5 {
			result, err = auth.RequireH5(request.Context(), request.Header, override)
		}
		if err != nil {
			writeError(writer, err)
			return false
		}
		if rejected := access.RejectUnauthorized(result); rejected != nil {
			writeJSON(writer, rejected.Status, rejected.Body)
			return false
		}
	}
	return true
}
