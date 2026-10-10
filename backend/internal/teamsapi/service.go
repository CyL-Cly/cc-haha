// Package teamsapi ports the original REST routing contract. Durable plans
// and workbench readers are supplied by their actual shared native services.
package teamsapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string { return e.Message }

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "Go server capability is not implemented: teams " + e.Boundary
}

type Reader interface {
	ListTeams(context.Context) (json.RawMessage, error)
	GetTeam(context.Context, string) (protocol.JSONObject, error)
	GetWorkbench(context.Context, string) (protocol.JSONObject, error)
	GetWorkbenchForSession(context.Context, string, protocol.JSONObject) (*protocol.JSONObject, error)
	GetMemberTranscriptPage(context.Context, string, string, protocol.JSONObject) (protocol.JSONObject, error)
	SendMemberMessage(context.Context, string, string, json.RawMessage) error
	DeleteTeam(context.Context, string) error
}
type Options struct {
	Plans  *teamplan.Service
	Reader Reader
}
type Service struct{ options Options }

func New(o Options) (*Service, error) {
	if o.Plans == nil || o.Reader == nil {
		return nil, errors.New("Teams API requires actual plan approval runtime and workbench/transcript reader")
	}
	return &Service{options: o}, nil
}
func raw(v any) json.RawMessage                { data, _ := json.Marshal(v); return data }
func field(k string, v any) protocol.JSONField { return protocol.JSONField{Name: k, Value: raw(v)} }
func obj(data json.RawMessage) protocol.JSONObject {
	var v protocol.JSONObject
	_ = v.UnmarshalJSON(data)
	return v
}
func bad(message string) error { return &APIError{400, "BAD_REQUEST", message} }
func pathSegment(segments []string, i int) (string, error) {
	if i >= len(segments) || segments[i] == "" {
		return "", nil
	}
	v, e := url.PathUnescape(segments[i])
	if e != nil || !utf8.ValidString(v) {
		return "", errors.New("URI malformed")
	}
	return v, nil
}
func at(parts []string, i int) string {
	if i >= len(parts) {
		return ""
	}
	return parts[i]
}
func (s *Service) Handle(ctx context.Context, method string, u *url.URL, parts []string, body []byte) (protocol.JSONObject, error) {
	name, err := pathSegment(parts, 2)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var result protocol.JSONObject
	if method == "GET" && name == "session" && at(parts, 3) != "" && at(parts, 4) == "plan" {
		id, err := pathSegment(parts, 3)
		if err != nil {
			return result, err
		}
		plan, err := s.options.Plans.GetForSession(ctx, id)
		return protocol.NewJSONObject(field("plan", plan)), err
	}
	if name != "" && at(parts, 3) == "plan" && (method == "PATCH" || method == "POST") {
		parsed, err := protocol.ParseAndStringifyJavaScriptJSON(body)
		if err != nil {
			return result, bad("Invalid JSON body")
		}
		if method == "PATCH" {
			request, err := teamplan.ParsePatchRequest(parsed)
			if err != nil {
				return result, bad("Invalid team plan update")
			}
			patch := protocol.NewJSONObject()
			for _, key := range []string{"members", "tasks"} {
				patch.Set(key, request.Get(key))
				request.Delete(key)
			}
			plan, err := s.options.Plans.Update(ctx, name, teamplan.IdentityFrom(request), patch)
			return protocol.NewJSONObject(field("plan", plan)), err
		}
		action, err := teamplan.ParseAction(parsed)
		if err != nil {
			return result, bad("Invalid team plan action")
		}
		op := at(parts, 4)
		var plan protocol.JSONObject
		if op == "approve" {
			plan, err = s.options.Plans.Approve(ctx, name, action)
		} else if op == "return" || op == "cancel" || op == "retry" {
			plan, err = s.options.Plans.Action(ctx, name, op, action)
		} else {
			return result, bad("Unknown team plan action")
		}
		return protocol.NewJSONObject(field("plan", plan)), err
	}
	reader := s.options.Reader
	if method == "GET" && name == "" {
		teams, err := reader.ListTeams(ctx)
		return protocol.NewJSONObject(protocol.JSONField{Name: "teams", Value: teams}), err
	}
	if method == "GET" && name == "session" && at(parts, 3) != "" && at(parts, 4) == "workbench" {
		id, err := pathSegment(parts, 3)
		if err != nil {
			return result, err
		}
		lookup := protocol.NewJSONObject()
		query := u.Query()
		for _, key := range []string{"teamName", "incarnationId"} {
			if value := query.Get(key); value != "" {
				lookup.Set(key, raw(value))
			}
		}
		if values, ok := query["at"]; ok && len(values) > 0 {
			if n, ok := number(values[0]); ok && !math.IsInf(n, 0) && !math.IsNaN(n) {
				lookup.Set("at", raw(n))
			}
		}
		timeline, err := reader.GetWorkbenchForSession(ctx, id, lookup)
		if err != nil {
			return result, err
		}
		if timeline == nil {
			return result, &APIError{404, "NOT_FOUND", "No Agent Teams workbench for session: " + id}
		}
		return *timeline, nil
	}
	if method == "GET" && name != "" && at(parts, 3) == "members" && at(parts, 4) != "" && at(parts, 5) == "transcript" {
		id, err := pathSegment(parts, 4)
		if err != nil {
			return result, err
		}
		options := protocol.NewJSONObject()
		query := u.Query()
		incremental := query.Get("incremental") == "true"
		for _, key := range []string{"leadSessionId", "incarnationId"} {
			if v := query.Get(key); v != "" {
				options.Set(key, raw(v))
			}
		}
		if incremental {
			for _, key := range []string{"signature", "cursor"} {
				if v := query.Get(key); v != "" {
					options.Set(key, raw(v))
				}
			}
			if values, ok := query["afterOrdinal"]; ok && len(values) > 0 {
				prefix := integerPrefix.FindString(protocol.TrimJavaScriptString(values[0]))
				if prefix != "" {
					n, err := strconv.ParseFloat(prefix, 64)
					if err == nil && math.Abs(n) <= 9007199254740991 {
						options.Set("afterOrdinal", raw(n))
					}
				}
			}
		}
		page, err := reader.GetMemberTranscriptPage(ctx, name, id, options)
		if err != nil || incremental {
			return page, err
		}
		projected := protocol.NewJSONObject()
		for _, key := range []string{"messages", "ownerAgentIds", "taskNotifications", "taskAnchors"} {
			projected.Set(key, page.Get(key))
		}
		return projected, nil
	}
	if method == "GET" && name != "" && at(parts, 3) == "workbench" {
		return reader.GetWorkbench(ctx, name)
	}
	if method == "POST" && name != "" && at(parts, 3) == "members" && at(parts, 4) != "" && at(parts, 5) == "messages" {
		id, err := pathSegment(parts, 4)
		if err != nil {
			return result, err
		}
		parsed, err := protocol.ParseAndStringifyJavaScriptJSON(body)
		if err != nil {
			return result, bad("Invalid JSON body")
		}
		if string(parsed) == "null" {
			return result, errors.New("Cannot read properties of null (reading 'content')")
		}
		content := obj(parsed).Get("content")
		if content == nil || string(content) == "null" {
			content = raw("")
		}
		if err = reader.SendMemberMessage(ctx, name, id, content); err != nil {
			return result, err
		}
		return protocol.NewJSONObject(field("ok", true)), nil
	}
	if method == "GET" && name != "" {
		return reader.GetTeam(ctx, name)
	}
	if method == "DELETE" && name != "" {
		if err = reader.DeleteTeam(ctx, name); err != nil {
			return result, err
		}
		return protocol.NewJSONObject(field("ok", true)), nil
	}
	suffix := ""
	if name != "" {
		suffix = "/" + name
	}
	return result, &APIError{405, "METHOD_NOT_ALLOWED", "Method " + method + " not allowed on /api/teams" + suffix}
}

var integerPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

func number(value string) (float64, bool) {
	value = protocol.TrimJavaScriptString(value)
	if value == "" {
		return 0, true
	}
	if len(value) > 2 && value[0] == '0' {
		base := 0
		switch value[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base > 0 {
			n, ok := new(big.Int).SetString(value[2:], base)
			if !ok || strings.ContainsAny(value[2:], "+-_") {
				return 0, false
			}
			result, _ := new(big.Float).SetInt(n).Float64()
			return result, true
		}
	}
	if strings.ContainsAny(value, "pP_") || value == "Inf" || value == "+Inf" || value == "-Inf" {
		return 0, false
	}
	n, err := strconv.ParseFloat(value, 64)
	return n, err == nil
}
