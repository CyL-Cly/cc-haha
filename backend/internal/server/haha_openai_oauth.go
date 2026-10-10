package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Server) hahaOpenAIOAuthHTTP(w http.ResponseWriter, r *http.Request, parts []string) (err error) {
	defer func() {
		var api *apiError
		if err != nil && !errors.As(err, &api) {
			err = &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
		}
	}()
	service := s.options.HahaOpenAIOAuth
	if service == nil {
		return unsupported("haha-openai-oauth")
	}
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}
	if action == "start" && r.Method == "POST" {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return badRequest("Invalid JSON body")
		}
		normalized, err := protocol.ParseAndStringifyJavaScriptJSON(protocol.DecodeJavaScriptUTF8(data))
		if err != nil {
			return badRequest("Invalid JSON body")
		}
		var body protocol.JSONObject
		if body.UnmarshalJSON(normalized) != nil || !body.IsObject() {
			return badRequest("serverPort (positive integer) required")
		}
		var port float64
		value := body.Get("serverPort")
		if len(value) == 0 || value[0] == '"' || json.Unmarshal(value, &port) != nil || math.IsNaN(port) || math.IsInf(port, 0) || port <= 0 || math.Trunc(port) != port {
			return badRequest("serverPort (positive integer) required")
		}
		session, err := service.StartSession(port)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]string{"authorizeUrl": session.AuthorizeURL, "state": session.State})
		return nil
	}
	if action == "" && r.Method == "GET" {
		// Source only settles an aborted HTTP response; account refresh and its
		// already-started persistence stay owned by the service lifecycle.
		tokens, err := service.EnsureFreshTokens(context.WithoutCancel(r.Context()))
		if err != nil {
			return err
		}
		body := protocol.NewJSONObject(protocol.JSONField{Name: "loggedIn", Value: []byte("false")})
		if tokens != nil {
			body.Set("loggedIn", []byte("true"))
			record := tokens.Record()
			for _, name := range []string{"expiresAt", "email", "accountId"} {
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

func (s *Server) hahaOpenAIOAuthCallback(w http.ResponseWriter, r *http.Request) error {
	if s.options.HahaOpenAIOAuth == nil {
		return unsupported("haha-openai-oauth callback")
	}
	query := r.URL.Query()
	message := ""
	success := false
	if value := query.Get("error"); value != "" {
		message = "OAuth provider returned: " + value
	} else if query.Get("code") == "" || query.Get("state") == "" {
		message = "Missing code or state parameter"
	} else {
		_, err := s.options.HahaOpenAIOAuth.CompleteSession(context.WithoutCancel(r.Context()), query.Get("code"), query.Get("state"))
		if err == nil {
			success = true
		} else {
			message = err.Error()
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	_, _ = io.WriteString(w, desktopoauth.OpenAICallbackPage(success, message, true))
	return nil
}
