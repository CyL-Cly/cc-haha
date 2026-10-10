package server

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The service is passed explicitly so the top-level server owner can bind one
// account/session/refresh producer to API, callback and model/runtime consumers.
func (s *Server) hahaOAuthHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *desktopoauth.Service) error {
	if service == nil {
		return unsupported("haha-oauth")
	}
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}
	if action == "start" && r.Method == "POST" {
		data, err := io.ReadAll(r.Body)
		if err != nil || !json.Valid(data) {
			return badRequest("Invalid JSON body")
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(data, &body) != nil || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
			return badRequest("serverPort (positive integer) required")
		}
		var port float64
		if value := body["serverPort"]; len(value) == 0 || value[0] == '"' || json.Unmarshal(value, &port) != nil || port <= 0 || math.Trunc(port) != port {
			return badRequest("serverPort (positive integer) required")
		}
		session, err := service.StartSession(port)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"authorizeUrl": session.AuthorizeURL, "state": session.State})
		return nil
	}
	if action == "callback" && r.Method == "GET" {
		return s.hahaOAuthCallback(w, r, service)
	}
	if (action == "" || action == "status") && r.Method == "GET" {
		tokens, err := service.EnsureFreshTokens(r.Context())
		if err != nil {
			return err
		}
		body := protocol.NewJSONObject(protocol.JSONField{Name: "loggedIn", Value: []byte("false")})
		if tokens != nil {
			body.Set("loggedIn", []byte("true"))
			record := tokens.Record()
			for _, name := range []string{"expiresAt", "scopes", "subscriptionType"} {
				body.Set(name, record.Get(name))
			}
		}
		writeJSON(w, 200, body)
		return nil
	}
	if action == "" && r.Method == "DELETE" {
		if err := service.DeleteTokens(r.Context()); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}
	writeJSON(w, 404, map[string]string{"error": "Not Found"})
	return nil
}
func (s *Server) hahaOAuthCallback(w http.ResponseWriter, r *http.Request, service *desktopoauth.Service) error {
	if service == nil {
		return unsupported("haha-oauth callback")
	}
	query := r.URL.Query()
	message := ""
	success := false
	if value := query.Get("error"); value != "" {
		message = "OAuth provider returned: " + value
	} else if query.Get("code") == "" || query.Get("state") == "" {
		message = "Missing code or state parameter"
	} else {
		_, err := service.CompleteSession(r.Context(), query.Get("code"), query.Get("state"))
		if err == nil {
			success = true
		} else {
			message = err.Error()
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	_, _ = io.WriteString(w, claudeOAuthCallbackPage(success, message))
	return nil
}
func claudeOAuthCallbackPage(success bool, message string) string {
	if success {
		return `<!doctype html>
<html><head><meta charset="utf-8"><title>Login Success</title>
<style>body{font-family:-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#fafafa;color:#333}.card{text-align:center;padding:40px;background:white;border-radius:12px;box-shadow:0 4px 16px rgba(0,0,0,.06)}h1{color:#16a34a;margin:0 0 12px}p{color:#666}</style>
</head><body><div class="card"><h1>✓ Login Successful</h1><p>You can close this window and return to Claude Code Haha.</p></div>
<script>setTimeout(() => window.close(), 1500)</script>
</body></html>`
	}
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;").Replace(message)
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>Login Failed</title>
<style>body{font-family:-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#fafafa;color:#333}.card{text-align:center;padding:40px;background:white;border-radius:12px;box-shadow:0 4px 16px rgba(0,0,0,.06)}h1{color:#dc2626;margin:0 0 12px}pre{color:#666;white-space:pre-wrap;word-break:break-word;text-align:left;background:#f5f5f5;padding:12px;border-radius:6px}</style>
</head><body><div class="card"><h1>✗ Login Failed</h1><pre>` + escaped + `</pre></div>
</body></html>`
}
