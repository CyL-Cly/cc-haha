package sessioncollaboration

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func apiString(body protocol.JSONObject, name string, required bool) (json.RawMessage, error) {
	value := body.Get(name)
	if value == nil && !required {
		return nil, nil
	}
	if !stringField(body, name) || trim(body.String(name)) == "" || stringLength(value) > 32000 {
		return nil, bad(name + " must be a nonempty string of at most 32000 characters")
	}
	return value, nil
}
func apiNumber(body protocol.JSONObject, name string) (json.RawMessage, error) {
	value := body.Get(name)
	if value == nil {
		return nil, nil
	}
	var number float64
	if len(value) == 0 || value[0] == '"' || value[0] == 'n' || value[0] == '[' || value[0] == '{' || json.Unmarshal(value, &number) != nil || math.IsInf(number, 0) || math.IsNaN(number) || number < 0 || number != math.Trunc(number) {
		return nil, bad(name + " must be a nonnegative integer")
	}
	return value, nil
}

// HandleAction is the original SDK-token API boundary. The host resolves the
// caller from its registered session token before reaching this handler.
func (s *Service) HandleAction(request *http.Request, action, caller string) (protocol.JSONObject, error) {
	if request.Method != http.MethodPost {
		return object("error", "METHOD_NOT_ALLOWED"), &APIError{Status: 405, Code: "METHOD_NOT_ALLOWED"}
	}
	data, e := io.ReadAll(request.Body)
	if e != nil {
		return protocol.JSONObject{}, bad("A JSON object is required")
	}
	var body protocol.JSONObject
	if e = body.UnmarshalJSON(data); e != nil || !body.IsObject() {
		return protocol.JSONObject{}, bad("A JSON object is required")
	}
	ctx := request.Context()
	options := protocol.NewJSONObject()
	strings := func(required string, names ...string) error {
		for _, name := range names {
			value, e := apiString(body, name, name == required)
			if e != nil {
				return e
			}
			copyRaw(&options, name, value)
		}
		return nil
	}
	numbers := func(names ...string) error {
		for _, name := range names {
			value, e := apiNumber(body, name)
			if e != nil {
				return e
			}
			copyRaw(&options, name, value)
		}
		return nil
	}
	switch action {
	case "list":
		if e = strings("", "query"); e == nil {
			e = numbers("limit", "offset")
		}
		if e != nil {
			return protocol.JSONObject{}, e
		}
		return s.List(ctx, options)
	case "read":
		if e = strings("sessionId", "sessionId", "cursor"); e == nil {
			e = numbers("limit", "maxOutputCharsPerItem")
		}
		if e != nil {
			return protocol.JSONObject{}, e
		}
		options.Set("includeOutputs", raw(isTrue(body.Get("includeOutputs"))))
		return s.Read(ctx, options.String("sessionId"), options)
	case "create":
		provider := body.Get("providerId")
		if provider != nil && string(provider) != "null" && !stringField(body, "providerId") {
			return protocol.JSONObject{}, bad("providerId must be a string or null")
		}
		if e = strings("prompt", "prompt", "requestId", "title", "workDir", "model"); e != nil {
			return protocol.JSONObject{}, e
		}
		copyRaw(&options, "providerId", provider)
		return s.Create(ctx, caller, options)
	case "send":
		if e = strings("targetSessionId", "targetSessionId"); e == nil {
			e = strings("content", "content", "messageId")
		}
		if e != nil {
			return protocol.JSONObject{}, e
		}
		return s.Send(ctx, caller, options.String("targetSessionId"), options)
	case "wait":
		var sessionIDs []string
		if body.Get("sessionIds") != nil {
			var values []json.RawMessage
			if e := json.Unmarshal(body.Get("sessionIds"), &values); e != nil || values == nil || len(values) > 8 {
				return protocol.JSONObject{}, bad("sessionIds must contain at most eight session ids")
			}
			sessionIDs = []string{}
			for _, value := range values {
				var id string
				if e := json.Unmarshal(value, &id); e != nil || id == "" {
					return protocol.JSONObject{}, bad("sessionIds must contain at most eight session ids")
				}
				sessionIDs = append(sessionIDs, id)
			}
		}
		if e = numbers("afterRevision", "timeoutMs"); e != nil {
			return protocol.JSONObject{}, e
		}
		after := num(options.Get("afterRevision"))
		if options.Get("afterRevision") == nil {
			status, e := s.Status(ctx, nil)
			if e != nil {
				return protocol.JSONObject{}, e
			}
			after = num(status.Get("revision"))
		}
		requested := float64(WaitDefaultMS)
		if timeout := options.Get("timeoutMs"); timeout != nil {
			requested = num(timeout)
		}
		return s.WaitNumber(ctx, after, sessionIDs, requested, caller)
	default:
		return protocol.JSONObject{}, missing("Unknown session collaboration action")
	}
}

func (s *Service) HandleUI(ctx context.Context, method string, parts []string, query json.RawMessage) (protocol.JSONObject, error) {
	if len(parts) == 0 && method == http.MethodGet {
		return s.Candidates(ctx, query)
	}
	if len(parts) == 0 || parts[0] == "" {
		return protocol.JSONObject{}, missing("Session id required")
	}
	if len(parts) == 2 {
		switch {
		case parts[1] == "status" && method == http.MethodGet:
			return s.GroupStatus(ctx, parts[0])
		case parts[1] == "stop" && method == http.MethodPost:
			e := s.StopGroup(ctx, parts[0])
			return object("ok", true), e
		case parts[1] == "resume" && method == http.MethodPost:
			e := s.Resume(ctx, parts[0])
			return object("ok", true), e
		}
	}
	return protocol.JSONObject{}, missing("Unknown session collaboration UI route")
}
