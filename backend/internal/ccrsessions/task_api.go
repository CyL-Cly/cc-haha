package ccrsessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TaskAPI is the existing Sessions HTTP/auth transport for RemoteAgentTask's
// fetch/poll/archive consumers. Those consumers do not select a model, create a
// session, inspect Git, run eligibility policy or invoke a title producer.
type TaskAPI struct{ service *Service }

type TaskAPIOptions struct {
	BaseURL     string
	HTTP        *http.Client
	Credentials func(context.Context, bool) (Credentials, error)
	Report      func(error)
}

func NewTaskAPI(o TaskAPIOptions) (*TaskAPI, error) {
	u, err := url.Parse(o.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || o.HTTP == nil || o.Credentials == nil {
		return nil, errors.New("Remote task Sessions API requires the actual OAuth and HTTP producer")
	}
	life, cancel := context.WithCancel(context.Background())
	return &TaskAPI{service: &Service{options: Options{BaseURL: strings.TrimRight(o.BaseURL, "/"), HTTP: o.HTTP, Credentials: o.Credentials, Report: o.Report}, life: life, cancel: cancel}}, nil
}

func (api *TaskAPI) Close() { api.service.Close() }
func (api *TaskAPI) FetchSession(ctx context.Context, sid string) (protocol.JSONObject, error) {
	return api.service.FetchSession(ctx, sid)
}

type TaskPoll struct {
	Events                []protocol.JSONObject
	LastEventID           json.RawMessage
	SessionStatus, Branch json.RawMessage
}

// Poll follows the original paged /events consumer, then its separate metadata
// fetch. Metadata failure is diagnostic; it does not discard a fetched delta.
func (api *TaskAPI) Poll(ctx context.Context, sid string, afterID json.RawMessage) (TaskPoll, error) {
	s := api.service
	credentials, err := s.options.Credentials(ctx, false)
	if err != nil {
		return TaskPoll{}, err
	}
	if credentials.Token == "" {
		return TaskPoll{}, errors.New("No access token for polling")
	}
	if credentials.Organization == "" {
		return TaskPoll{}, errors.New("No org UUID for polling")
	}
	result := TaskPoll{Events: []protocol.JSONObject{}, LastEventID: append(json.RawMessage(nil), afterID...)}
	for page := 0; page < 50; page++ {
		path := "/v1/sessions/" + sid + "/events"
		if value := taskCursor(result.LastEventID); value != "" {
			path += "?after_id=" + url.QueryEscape(value)
		}
		body, status, err := s.request(ctx, "GET", path, nil, credentials, true, 30*time.Second, nil)
		if err != nil {
			return TaskPoll{}, err
		}
		if status < 200 || status >= 300 {
			return TaskPoll{}, fmt.Errorf("Request failed with status code %d", status)
		}
		if status != 200 {
			return TaskPoll{}, fmt.Errorf("Failed to fetch session events: %s", http.StatusText(status))
		}
		var entries []json.RawMessage
		if value := body.Get("data"); len(value) == 0 || json.Unmarshal(value, &entries) != nil || strings.TrimSpace(string(value)) == "null" {
			return TaskPoll{}, errors.New("Invalid events response")
		}
		for _, raw := range entries {
			var event protocol.JSONObject
			if event.UnmarshalJSON(raw) != nil || !event.IsObject() || event.Get("type") == nil || event.Get("session_id") == nil || event.String("type") == "env_manager_log" || event.String("type") == "control_response" {
				continue
			}
			result.Events = append(result.Events, event)
		}
		last := body.Get("last_id")
		if taskCursor(last) == "" {
			break
		}
		result.LastEventID = append(json.RawMessage(nil), last...)
		if !taskValueTruthy(body.Get("has_more")) {
			break
		}
	}
	metadata, err := s.FetchSession(ctx, sid)
	if err != nil {
		s.report(fmt.Errorf("teleport: failed to fetch session %s metadata: %v", sid, err))
	} else {
		result.SessionStatus = metadata.Get("session_status")
		if branch := Branch(metadata); branch != "" {
			result.Branch, _ = json.Marshal(branch)
		}
	}
	return result, nil
}

// Archive is teleport.tsx's best-effort task kill consumer. It does not execute
// the separate strict archiveRemoteSession API's OAuth refresh/retry policy.
func (api *TaskAPI) Archive(ctx context.Context, sid string) error {
	s := api.service
	credentials, err := s.options.Credentials(ctx, false)
	if err != nil {
		return err
	}
	if credentials.Token == "" || credentials.Organization == "" {
		return nil
	}
	_, status, err := s.request(ctx, "POST", "/v1/sessions/"+sid+"/archive", []byte("{}"), credentials, true, 10*time.Second, nil)
	if err != nil {
		return err
	}
	if status >= 500 {
		return fmt.Errorf("Request failed with status code %d", status)
	}
	return nil
}

func taskValueTruthy(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" || value == "false" || value == `""` {
		return false
	}
	if value[0] == '-' || value[0] >= '0' && value[0] <= '9' {
		number, err := strconv.ParseFloat(value, 64)
		return (err == nil || math.IsInf(number, 0)) && number != 0 && !math.IsNaN(number)
	}
	return true
}
func taskCursor(raw json.RawMessage) string {
	if !taskValueTruthy(raw) {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}
