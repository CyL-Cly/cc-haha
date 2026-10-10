package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func responseField(key string, value any) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: key, Value: raw(value)})
}
func parseBody(body []byte) (protocol.JSONObject, error) {
	if !json.Valid(body) {
		return protocol.JSONObject{}, bad("Invalid JSON body")
	}
	body = bytes.TrimSpace(body)
	if string(body) == "null" {
		return protocol.JSONObject{}, errors.New("null scheduled task body")
	}
	var object protocol.JSONObject
	if object.UnmarshalJSON(body) == nil && object.IsObject() {
		return object, nil
	}
	object = protocol.NewJSONObject()
	if body[0] == '[' {
		var values []json.RawMessage
		if e := json.Unmarshal(body, &values); e != nil {
			return object, e
		}
		for n, value := range values {
			object.Set(strconv.Itoa(n), value)
		}
	}
	if body[0] == '"' {
		units, e := protocol.DecodeJavaScriptJSONString(body)
		if e != nil {
			return object, e
		}
		for n, unit := range units {
			object.Set(strconv.Itoa(n), protocol.QuoteJavaScriptUTF16([]uint16{unit}))
		}
	}
	return object, nil
}
func queryPage(u *url.URL, taskID string) (PageOptions, error) {
	q := u.Query()
	limitValue := q.Get("limit")
	if limitValue == "" {
		limitValue = "50"
	}
	limit := parseIntegerPrefix(limitValue)
	o := PageOptions{TaskID: taskID, Limit: &limit, Cursor: q.Get("cursor"), SummaryOnly: q.Get("summaryOnly") == "true", NonterminalOnly: q.Get("nonterminalOnly") == "true"}
	if q.Has("completedAfterMs") {
		value := jsNumberString(q.Get("completedAfterMs"))
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return o, bad("Invalid completedAfterMs parameter")
		}
		o.CompletedAfterMs = &value
	}
	return o, nil
}
func (s *Service) Handle(ctx context.Context, method string, u *url.URL, parts []string, body []byte) (int, protocol.JSONObject, error) {
	id, sub := "", ""
	if len(parts) > 2 {
		id = parts[2]
	}
	if len(parts) > 3 {
		sub = parts[3]
	}
	if method == "GET" && id == "runs" && sub != "" {
		run, e := s.GetRunDetail(ctx, sub)
		if e != nil {
			return 0, protocol.JSONObject{}, e
		}
		if run == nil {
			return 0, protocol.JSONObject{}, missing("Scheduled run " + sub + " not found")
		}
		return 200, responseField("run", run), nil
	}
	if method == "GET" && (id == "runs" || id != "" && sub == "runs") {
		taskID := id
		if id == "runs" {
			taskID = ""
		}
		o, e := queryPage(u, taskID)
		if e != nil {
			return 0, protocol.JSONObject{}, e
		}
		if o.Cursor == "" && !o.SummaryOnly && !o.NonterminalOnly && o.CompletedAfterMs == nil {
			if id == "runs" {
				rows, e := s.GetRecentRuns(ctx, *o.Limit)
				return 200, responseField("runs", rows), e
			}
			if u.Query().Get("limit") == "" {
				rows, e := s.GetTaskRuns(ctx, id)
				return 200, responseField("runs", rows), e
			}
		}
		page, e := s.GetRunsPage(ctx, o)
		var out protocol.JSONObject
		b, _ := json.Marshal(page)
		_ = out.UnmarshalJSON(b)
		return 200, out, e
	}
	if method == "GET" && id == "" {
		tasks, e := s.ListTasks(ctx)
		return 200, responseField("tasks", tasks), e
	}
	if method == "POST" && id == "" {
		object, e := parseBody(body)
		if e != nil {
			return 0, protocol.JSONObject{}, e
		}
		input := protocol.NewJSONObject()
		for _, key := range []string{"name", "description", "cron", "prompt", "enabled", "recurring", "permanent", "permissionMode", "model", "providerId", "folderPath", "useWorktree", "notification"} {
			if v := object.Get(key); v != nil {
				input.Set(key, v)
			}
		}
		task, e := s.CreateTask(ctx, input)
		return 201, responseField("task", task), e
	}
	if method == "POST" && id != "" && sub == "run" {
		if e := s.RunNow(ctx, id); e != nil {
			return 0, protocol.JSONObject{}, e
		}
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return 0, protocol.JSONObject{}, context.Cause(ctx)
		}
		return 200, responseField("ok", true), nil
	}
	if method == "PUT" && id != "" && sub == "" {
		object, e := parseBody(body)
		if e != nil {
			return 0, protocol.JSONObject{}, e
		}
		task, e := s.UpdateTask(ctx, id, object)
		return 200, responseField("task", task), e
	}
	if method == "DELETE" && id != "" && sub == "" {
		e := s.DeleteTask(ctx, id)
		return 200, responseField("ok", true), e
	}
	path := "/api/scheduled-tasks"
	if id != "" {
		path += "/" + id
	}
	if sub != "" {
		path += "/" + sub
	}
	return 0, protocol.JSONObject{}, &Error{405, "METHOD_NOT_ALLOWED", "Method " + strings.ToUpper(method) + " not allowed on " + path}
}
