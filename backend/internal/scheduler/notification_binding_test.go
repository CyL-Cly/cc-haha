package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSchedulerDefaultNotifierActuallyLoadsPrivateRawConfigAndStopDrainsDelivery(t *testing.T) {
	received := make(chan []byte, 2)
	entered := make(chan struct{})
	release := make(chan struct{})
	delivered := make(chan struct{})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		received <- data
		close(entered)
		<-release
		_, _ = io.WriteString(w, `{"ok":true}`)
		close(delivered)
	}))
	defer host.Close()
	o := disposableOptions(t)
	if e := os.WriteFile(filepath.Join(o.ConfigDir, "adapters.json"), []byte(`{"telegram":{"botToken":"fake-private-token","allowedUsers":[123]},"unknown":"preserved"}`), 0600); e != nil {
		t.Fatal(e)
	}
	reader, e := adapterconfig.New(adapterconfig.Options{ConfigDir: o.ConfigDir, ReadFile: os.ReadFile, LogError: func(e error) { t.Error(e) }})
	if e != nil {
		t.Fatal(e)
	}
	o.GetRawAdapterConfig = reader.GetRawConfig
	o.NotificationHTTPDo = func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.telegram.org" || !strings.Contains(request.URL.Path, "fake-private-token") {
			t.Error("raw secret masked/changed", request.URL)
		}
		request = request.Clone(request.Context())
		request.URL, _ = url.Parse(host.URL + request.URL.Path)
		request.Host = request.URL.Host
		return host.Client().Do(request)
	}
	o.NotificationLog = func(level, message string, value any) {
		t.Errorf("unexpected notifier log %s %s %v", level, message, value)
	}
	o.Execute = func(_ context.Context, request ExecutionRequest, emit func(json.RawMessage)) (ExecutionResult, error) {
		emit([]byte(`{"type":"result","result":"actual shared output"}`))
		return ExecutionResult{}, nil
	}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "cron", Value: quoted("* * * * *")}, protocol.JSONField{Name: "prompt", Value: quoted("deliver completion")}, protocol.JSONField{Name: "recurring", Value: []byte(`true`)}, protocol.JSONField{Name: "notification", Value: []byte(`{"enabled":true,"channels":["desktop","telegram"]}`)})
	task, e := s.CreateTask(context.Background(), input)
	if e != nil {
		t.Fatal(e)
	}
	run, e := s.ExecuteTask(context.Background(), task, false)
	if e != nil || run.String("status") != "completed" {
		t.Fatal(run, e)
	}
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- s.Stop(context.Background()) }()
	select {
	case e := <-stopped:
		t.Fatal("shutdown detached notification", e)
	default:
	}
	close(release)
	if e = <-stopped; e != nil {
		t.Fatal(e)
	}
	select {
	case <-delivered:
	default:
		t.Fatal("shutdown did not drain response")
	}
	data := <-received
	var payload protocol.JSONObject
	if e = payload.UnmarshalJSON(data); e != nil || string(payload.Get("chat_id")) != "123" || !strings.Contains(payload.String("text"), "actual shared output") {
		t.Fatal("real notification body lost actual result", string(data), e)
	}
	after, _ := os.ReadFile(filepath.Join(o.ConfigDir, "adapters.json"))
	if !strings.Contains(string(after), `"unknown":"preserved"`) {
		t.Fatal("notifier rewrote private config")
	}
}
