package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSchedulerNotificationsActualSourceHTTPBodiesOrderingLimitsAndFailures(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["app_id"] == "bad-app" {
				_, _ = io.WriteString(w, `{"code":1,"msg":"fixture denial"}`)
			} else {
				_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"fake-fixture-tenant"}`)
			}
			return
		}
		if strings.Contains(r.URL.Path, "botbad-token") {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, "fixture Telegram failed")
			return
		}
		if strings.Contains(r.URL.Path, "messages") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["receive_id"] == "fail-user" {
				w.WriteHeader(500)
				_, _ = io.WriteString(w, "fixture Feishu failed")
				return
			}
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer host.Close()
	run := map[string]any{"id": "run", "taskId": "task", "taskName": "定时任务", "status": "completed", "durationMs": 119999, "output": "actual result"}
	cfg := map[string]any{"telegram": map[string]any{"botToken": "fake-token", "pairedUsers": []any{map[string]any{"userId": 123}}, "allowedUsers": []any{123, "user"}}, "feishu": map[string]any{"appId": "fake-app", "appSecret": "fake-secret", "domain": "lark", "pairedUsers": []any{map[string]any{"userId": "open-user"}}, "allowedUsers": []any{"open-user", 42}}}
	notification := map[string]any{"enabled": true, "channels": []any{"desktop", "telegram", "feishu", "telegram"}}
	rows := []map[string]any{{"config": cfg, "run": run, "notification": notification}, {"config": map[string]any{}, "run": run, "notification": notification}, {"config": cfg, "run": run, "notification": map[string]any{"enabled": false, "channels": []any{"telegram"}}}, {"config": cfg, "run": run, "notification": map[string]any{"enabled": true, "channels": []any{"desktop"}}}}
	for _, duration := range []float64{0, 1, 500, 59999, 60000, 119999, 120000} {
		rows = append(rows, map[string]any{"config": cfg, "run": map[string]any{"taskName": "duration", "status": "failed", "durationMs": duration, "error": strings.Repeat("错", 501), "output": strings.Repeat("😀", 2100)}, "notification": notification})
	}
	rows = append(rows, map[string]any{"config": map[string]any{"telegram": map[string]any{"botToken": "bad-token", "allowedUsers": []any{1, 2}}, "feishu": map[string]any{"appId": "bad-app", "appSecret": "fake-secret", "allowedUsers": []any{"unused"}}}, "run": run, "notification": notification}, map[string]any{"config": map[string]any{"feishu": map[string]any{"appId": "fake-app", "appSecret": "fake-secret", "allowedUsers": []any{"fail-user", "next-user"}}}, "run": map[string]any{"taskName": "timeout", "status": "timeout"}, "notification": notification})
	data := actualSource(t, "notification", map[string]any{"origin": host.URL, "rows": rows})
	var source struct {
		Rows []struct {
			Requests []json.RawMessage
			Logs     []json.RawMessage
			Error    string
		}
	}
	if e := json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	for n, item := range rows {
		var mu sync.Mutex
		requests := []json.RawMessage{}
		logs := []json.RawMessage{}
		notifier, e := NewNotifier(NotificationOptions{GetRawConfig: func(context.Context) (protocol.JSONObject, error) {
			var out protocol.JSONObject
			_ = out.UnmarshalJSON(raw(item["config"]))
			return out, nil
		}, Do: func(request *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(request.Body)
			var payload protocol.JSONObject
			_ = payload.UnmarshalJSON(b)
			headers := protocol.NewJSONObject(protocol.JSONField{Name: "Content-Type", Value: quoted(request.Header.Get("Content-Type"))})
			if v := request.Header.Get("Authorization"); v != "" {
				headers.Set("Authorization", quoted(v))
			}
			mu.Lock()
			requests = append(requests, raw(protocol.NewJSONObject(protocol.JSONField{Name: "url", Value: quoted(request.URL.String())}, protocol.JSONField{Name: "method", Value: quoted(request.Method)}, protocol.JSONField{Name: "headers", Value: raw(headers)}, protocol.JSONField{Name: "body", Value: raw(payload)})))
			mu.Unlock()
			original := request.URL
			prefix := "/telegram"
			if original.Host == "open.feishu.cn" {
				prefix = "/feishu"
			}
			u, _ := url.Parse(host.URL + prefix + original.Path + "?" + original.RawQuery)
			request = request.Clone(request.Context())
			request.URL = u
			request.Host = u.Host
			request.Body = io.NopCloser(strings.NewReader(string(b)))
			return host.Client().Do(request)
		}, Log: func(level, message string, details any) {
			if err, ok := details.(error); ok {
				details = err.Error()
			}
			row := protocol.NewJSONObject(protocol.JSONField{Name: "level", Value: quoted(level)}, protocol.JSONField{Name: "message", Value: quoted(message)})
			if details != nil {
				row.Set("details", raw(details))
			}
			logs = append(logs, raw(row))
		}})
		if e != nil {
			t.Fatal(e)
		}
		var run, notification protocol.JSONObject
		_ = run.UnmarshalJSON(raw(item["run"]))
		_ = notification.UnmarshalJSON(raw(item["notification"]))
		e = notifier.Send(context.Background(), run, notification)
		if e != nil || source.Rows[n].Error != "" || !equalJSON(raw(requests), raw(source.Rows[n].Requests)) || !equalJSON(raw(logs), raw(source.Rows[n].Logs)) {
			t.Errorf("notification%d requests/logs differ Go%v Source%v requests=%d/%d logs=%s/%s", n, e, source.Rows[n].Error, len(requests), len(source.Rows[n].Requests), raw(logs), raw(source.Rows[n].Logs))
			if len(requests) > 0 && len(source.Rows[n].Requests) > 0 {
				t.Logf("first Go%.2000s Source%.2000s", requests[0], source.Rows[n].Requests[0])
			}
		}
	}
}
