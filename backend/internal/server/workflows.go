package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

func workflowError(err error) error {
	var own *workflow.APIError
	var capability *workflow.CapabilityError
	var commandCapability *workflowcommands.CapabilityError
	switch {
	case errors.As(err, &own):
		return &apiError{own.Status, own.Code, own.Message}
	case errors.As(err, &capability), errors.As(err, &commandCapability):
		return unsupported(err.Error())
	}
	return err
}
func queryString(values url.Values, key string) *string {
	if !values.Has(key) {
		return nil
	}
	value := values.Get(key)
	return &value
}
func workflowScope(raw json.RawMessage) (string, error) {
	if raw == nil || string(raw) == "null" || string(raw) == `""` || string(raw) == `"user"` {
		return "user", nil
	}
	if string(raw) == `"project"` {
		return "project", nil
	}
	var value string
	converted, e := protocol.JavaScriptStringJSON(raw)
	if e != nil {
		return "", e
	}
	_ = json.Unmarshal(converted, &value)
	return "", badRequest("Invalid scope: " + value)
}

var workflowLimitPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

func workflowLimit(value string) *int {
	match := workflowLimitPrefix.FindString(strings.TrimSpace(value))
	number, e := strconv.ParseInt(match, 10, 64)
	if e != nil || number <= 0 || number > 9007199254740991 {
		return nil
	}
	limit := int(number)
	return &limit
}
func workflowBody(request *http.Request) (protocol.JSONObject, error) {
	raw, e := io.ReadAll(request.Body)
	if e != nil || !json.Valid(raw) {
		return protocol.JSONObject{}, badRequest("Invalid JSON body")
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return protocol.JSONObject{}, errors.New("null workflow request body")
	}
	var body protocol.JSONObject
	_ = json.Unmarshal(raw, &body)
	return body, nil
}

func (server *Server) workflowsHTTP(writer http.ResponseWriter, request *http.Request, parts []string) error {
	if server.options.Workflows == nil {
		return unsupported("workflows")
	}
	first := ""
	if len(parts) > 2 {
		var e error
		first, e = url.PathUnescape(parts[2])
		if e != nil {
			return e
		}
	}
	query := request.URL.Query()
	service := server.options.Workflows
	ctx := request.Context()
	var result any
	var err error
	switch {
	case request.Method == http.MethodGet && first == "":
		var values []protocol.JSONObject
		values, err = service.ListDefinitions(ctx, queryString(query, "cwd"))
		result = map[string]any{"workflows": values}
	case request.Method == http.MethodGet && first == "runs" && len(parts) < 4:
		var values []protocol.JSONObject
		values, err = service.ListRuns(ctx, queryString(query, "sessionId"), workflowLimit(query.Get("limit")))
		result = map[string]any{"runs": values}
	case request.Method == http.MethodGet && first == "session-runs" && len(parts) > 3:
		id, e := url.PathUnescape(parts[3])
		if e != nil {
			return e
		}
		var values []protocol.JSONObject
		values, err = service.ReconstructSessionRuns(ctx, id)
		result = map[string]any{"runs": values}
	case request.Method == http.MethodGet && first == "runs" && len(parts) > 4:
		id, e := url.PathUnescape(parts[3])
		if e != nil {
			return e
		}
		run, e := url.PathUnescape(parts[4])
		if e != nil {
			return e
		}
		result, err = service.GetRun(ctx, id, run)
	case request.Method == http.MethodPost && (first == "validate" || first == "save"):
		body, e := workflowBody(request)
		if e != nil {
			return e
		}
		if _, e := protocol.DecodeJavaScriptJSONString(body.Get("script")); e != nil {
			return badRequest("`script` is required")
		}
		if first == "validate" {
			result, err = service.Validate(ctx, body.Get("script"))
			break
		}
		if name := body.Get("name"); name != nil {
			if _, e := protocol.DecodeJavaScriptJSONString(name); e != nil {
				return badRequest("`name` must be a string")
			}
		}
		scope, e := workflowScope(body.Get("scope"))
		if e != nil {
			return e
		}
		var cwd *string
		if raw := body.Get("cwd"); raw != nil && string(raw) != "null" {
			value := body.String("cwd")
			if _, e := protocol.DecodeJavaScriptJSONString(raw); e != nil {
				return errors.New("invalid workflow cwd type")
			}
			cwd = &value
		}
		var saved protocol.JSONObject
		saved, err = service.SaveDefinition(ctx, body.Get("script"), scope, cwd, body.Get("name"))
		if err == nil {
			value := protocol.NewJSONObject(protocol.JSONField{Name: "ok", Value: []byte("true")})
			value.Assign(saved)
			result = value
		}
	case request.Method == http.MethodGet && first != "":
		result, err = service.GetDefinition(ctx, first, queryString(query, "cwd"))
	case request.Method == http.MethodDelete && first != "":
		raw := json.RawMessage(nil)
		if query.Has("scope") {
			raw = jsonString(query.Get("scope"))
		}
		scope, e := workflowScope(raw)
		if e != nil {
			return e
		}
		err = service.DeleteDefinition(ctx, first, scope, queryString(query, "cwd"))
		result = map[string]bool{"ok": true}
	default:
		suffix := ""
		if first != "" {
			suffix = "/" + first
		}
		return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + request.Method + " not allowed on /api/workflows" + suffix}
	}
	if err != nil {
		return workflowError(err)
	}
	writeJSON(writer, 200, result)
	return nil
}

// WorkflowTaskNotifications reuses the actual server transcript interpreter,
// including the original notification identity and overwrite order.
func (server *Server) WorkflowTaskNotifications(ctx context.Context, id string) ([]protocol.JSONObject, error) {
	info, e := server.findSession(ctx, id)
	if e != nil {
		return nil, e
	}
	if info.memory {
		return []protocol.JSONObject{}, nil
	}
	entries := []sessionstore.Entry{}
	retained := 0
	e = server.budget.With(ctx, sessionstore.MetadataLane, func() error {
		_, e := server.metadata.Stream(ctx, info.Path, func(value sessionmetadata.Entry, complete bool, _ int64) error {
			if !complete {
				return &workflow.APIError{Status: 503, Message: "Workflow history exceeds the bounded reconstruction budget", Code: "WORKFLOW_HISTORY_INCOMPLETE"}
			}
			if value.TextTruncated {
				return &workflow.APIError{Status: 503, Message: "Workflow history exceeds the bounded reconstruction budget", Code: "WORKFLOW_HISTORY_INCOMPLETE"}
			}
			if taskNotification(historyRecord(value.Value)) == nil {
				return nil
			}
			retained += len(value.Value)
			if retained > 3*1024*1024 || len(entries) >= 10000 {
				return &workflow.APIError{Status: 503, Message: "Workflow history exceeds the bounded reconstruction budget", Code: "WORKFLOW_HISTORY_INCOMPLETE"}
			}
			entries = append(entries, sessionstore.Entry{Entry: value.Value})
			return nil
		}, sessionmetadata.Options{})
		return e
	})
	if e != nil {
		return nil, e
	}
	_, notifications := projectHistory(entries)
	out := make([]protocol.JSONObject, 0, len(notifications))
	for _, notice := range notifications {
		raw, e := json.Marshal(notice)
		if e != nil {
			return nil, e
		}
		var object protocol.JSONObject
		if e := json.Unmarshal(raw, &object); e != nil {
			return nil, e
		}
		out = append(out, object)
	}
	return out, nil
}
