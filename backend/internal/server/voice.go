package server

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
	"io"
	"net/http"
	"net/url"
)

func (s *Server) voiceHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *voice.Service) error {
	if service == nil {
		return unsupported("voice")
	}
	err := s.handleVoiceHTTP(w, r, parts, service)
	if own, ok := err.(*voice.Error); ok {
		return &apiError{status: own.Status, code: own.Code, message: own.Message}
	}
	return err
}
func (s *Server) handleVoiceHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *voice.Service) error {
	method := func(expected string) error {
		if r.Method != expected {
			return methodNotAllowed(r.Method)
		}
		return nil
	}
	sub := "undefined"
	if len(parts) > 2 {
		sub = parts[2]
	}
	switch sub {
	case "catalog":
		if e := method("GET"); e != nil {
			return e
		}
		value, e := service.Catalog(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, value)
		return nil
	case "preferences":
		if e := method("PUT"); e != nil {
			return e
		}
		data, e := io.ReadAll(r.Body)
		if e != nil || !json.Valid(data) {
			return badRequest("Invalid JSON body")
		}
		var patch protocol.JSONObject
		_ = patch.UnmarshalJSON(data)
		value, e := service.UpdatePreferences(r.Context(), patch)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"preferences": value})
		return nil
	case "providers":
		if len(parts) < 5 || parts[3] == "" || parts[4] == "" {
			return notFound("Unknown voice provider endpoint")
		}
		id, e := url.PathUnescape(parts[3])
		if e != nil {
			return &voice.Error{Status: 404, Code: "voice/unknown-provider", Message: "Unknown voice provider"}
		}
		var result voice.Status
		switch parts[4] {
		case "status":
			if e = method("GET"); e == nil {
				result, e = service.Status(r.Context(), id)
			}
		case "prepare":
			if e = method("POST"); e == nil {
				result, e = service.Prepare(r.Context(), id)
			}
		case "cancel":
			if e = method("POST"); e == nil {
				result, e = service.Cancel(r.Context(), id)
			}
		case "assets":
			if e = method("DELETE"); e == nil {
				result, e = service.RemoveAssets(r.Context(), id)
			}
		default:
			return notFound("Unknown voice provider endpoint: " + parts[4])
		}
		if e != nil {
			return e
		}
		writeJSON(w, 200, result)
		return nil
	case "transcribe":
		if e := method("POST"); e != nil {
			return e
		}
		var language, provider *string
		if values, ok := r.URL.Query()["language"]; ok {
			language = &values[0]
			if !voice.Language(*language) {
				return badRequest("Unsupported language: " + *language)
			}
		}
		if values, ok := r.URL.Query()["provider"]; ok {
			provider = &values[0]
		}
		if r.ContentLength > voice.MaxAudioBytes {
			return &voice.Error{Status: 400, Code: "voice/invalid-audio", Message: fmt.Sprintf("Audio is too large (limit %d bytes)", voice.MaxAudioBytes)}
		}
		if r.Body == nil {
			return &voice.Error{Status: 400, Code: "voice/invalid-audio", Message: "Audio body is empty"}
		}
		wav, e := io.ReadAll(io.LimitReader(r.Body, voice.MaxAudioBytes+1))
		if e != nil {
			return e
		}
		if len(wav) > voice.MaxAudioBytes {
			return &voice.Error{Status: 400, Code: "voice/invalid-audio", Message: fmt.Sprintf("Audio is too large (limit %d bytes)", voice.MaxAudioBytes)}
		}
		result, e := service.Transcribe(requestSignalContext(r), provider, wav, language)
		if e != nil {
			return e
		}
		writeJSON(w, 200, result)
		return nil
	default:
		return notFound("Unknown voice endpoint: " + sub)
	}
}
