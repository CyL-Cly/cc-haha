package server

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Server) hahaGrokOAuthHTTP(w http.ResponseWriter, r *http.Request, parts []string) (err error) {
	defer func() {
		var api *apiError
		if err != nil && !errors.As(err, &api) {
			err = &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
		}
	}()
	service := s.options.HahaGrokOAuth
	if service == nil {
		return unsupported("haha-grok-oauth")
	}
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}
	if action == "start" && r.Method == "POST" {
		session, err := service.StartSession()
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]string{"authorizeUrl": session.AuthorizeURL, "state": session.State})
		return nil
	}
	if action == "success" && r.Method == "GET" {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, desktopoauth.GrokCallbackPage(true, ""))
		return nil
	}
	if action == "" && r.Method == "GET" {
		tokens, err := service.EnsureFreshTokens(context.WithoutCancel(r.Context()))
		if err != nil {
			return err
		}
		body := protocol.NewJSONObject(protocol.JSONField{Name: "loggedIn", Value: []byte("false")})
		if tokens != nil {
			body.Set("loggedIn", []byte("true"))
			record := tokens.Record()
			for _, name := range []string{"expiresAt", "email"} {
				body.Set(name, record.Get(name))
			}
		}
		writeJSON(w, 200, body)
		return nil
	}
	if action == "" && r.Method == "DELETE" {
		service.Dispose()
		if err := service.DeleteTokens(r.Context()); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}
	writeJSON(w, 404, map[string]string{"error": "Not Found"})
	return nil
}
