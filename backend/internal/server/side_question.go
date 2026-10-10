package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sidequestion"
)

type SideQuestionControlSession interface {
	RequestSideQuestionControl(context.Context, protocol.JSONObject, time.Duration) (protocol.JSONObject, error)
}

func (s *Server) getSideQuestions() (*sidequestion.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, context.Canceled
	}
	if s.sideQuestions != nil {
		return s.sideQuestions, nil
	}
	service, e := sidequestion.New(sidequestion.Options{Lifetime: s.ctx, Ensure: func(ctx context.Context, id string, _ *url.URL) error {
		_, e := s.live(id)
		return e
	}, Control: func(ctx context.Context, id string, request protocol.JSONObject, timeout time.Duration) (protocol.JSONObject, error) {
		s.mu.Lock()
		current := s.runtimes[id]
		s.mu.Unlock()
		if current == nil {
			return protocol.JSONObject{}, errors.New("CLI session is not running")
		}
		current.mu.Lock()
		live := current.live
		current.mu.Unlock()
		if consumer, ok := live.(SideQuestionControlSession); ok {
			return consumer.RequestSideQuestionControl(ctx, request, timeout)
		}
		return protocol.JSONObject{}, unsupported("actual side-question CLI control consumer")
	}})
	if e == nil {
		s.sideQuestions = service
	}
	return service, e
}
func (s *Server) sideQuestionHTTP(w http.ResponseWriter, r *http.Request, id string, parts []string) (failure error) {
	defer func() {
		var own *sidequestion.Error
		if errors.As(failure, &own) {
			if own.MessageJSON != nil {
				writeJSON(w, own.Status, protocol.NewJSONObject(protocol.JSONField{Name: "error", Value: func() json.RawMessage { value, _ := json.Marshal(own.Code); return value }()}, protocol.JSONField{Name: "message", Value: own.MessageJSON}))
				failure = nil
			} else {
				failure = &apiError{own.Status, own.Code, own.Message}
			}
		}
	}()
	if len(parts) > 5 {
		return notFound("Side question route not found")
	}
	s.mu.Lock()
	current := s.runtimes[id]
	s.mu.Unlock()
	active := false
	if current != nil {
		current.mu.Lock()
		active = current.live != nil
		current.mu.Unlock()
	}
	if !active {
		info, e := s.findSession(r.Context(), id)
		if e != nil {
			return notFound("Session not found: " + id)
		}
		if info.MessageCount == 0 {
			return &apiError{409, "CONFLICT", "Start a conversation before asking a side question"}
		}
	}
	questionID := ""
	if len(parts) > 4 {
		questionID = parts[4]
	}
	if r.Method == http.MethodPost && questionID == "" {
		if r.Body == nil || r.Body == http.NoBody {
			return badRequest("JSON body is required")
		}
		data, e := io.ReadAll(io.LimitReader(r.Body, 1024*1024+1))
		if e != nil {
			return e
		}
		if len(data) > 1024*1024 {
			_ = r.Body.Close()
			return &apiError{413, "BODY_TOO_LARGE", "Side question body is too large"}
		}
		parsed, e := protocol.ParseAndStringifyJavaScriptJSON(data)
		if e != nil {
			return badRequest("Invalid JSON body")
		}
		data = parsed
		input, e := sidequestion.Parse(data)
		if e != nil {
			return e
		}
		service, e := s.getSideQuestions()
		if e != nil {
			return e
		}
		response, e := service.Ask(requestSignalContext(r), id, input, r.URL)
		if e != nil {
			return e
		}
		writeJSON(w, 200, response)
		return nil
	}
	if r.Method == http.MethodDelete && questionID != "" {
		if !sidequestion.ValidUUID(questionID) {
			return badRequest("Invalid questionId")
		}
		service, e := s.getSideQuestions()
		if e != nil {
			return e
		}
		cancelled, e := service.Cancel(id, questionID)
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]any{"questionId": questionID, "cancelled": cancelled})
		return nil
	}
	writeJSON(w, 405, map[string]string{"error": "METHOD_NOT_ALLOWED", "message": "Unsupported side question route"})
	return nil
}
