package imruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type SlackAPIOptions struct {
	Token, BaseURL string
	HTTP           *http.Client
	Environment    map[string]string
}
type SlackAPI struct {
	Token, BaseURL string
	HTTP           *http.Client
}
type SlackAPIError struct {
	Method, Code string
	Status       int
}

func (e *SlackAPIError) Error() string { return "Slack " + e.Method + " failed: " + e.Code }
func NewSlackAPI(o SlackAPIOptions) (*SlackAPI, error) {
	if o.BaseURL == "" {
		o.BaseURL = "https://slack.com/api/"
	}
	if o.HTTP == nil {
		var err error
		o.HTTP, err = AdapterNetworkHTTP(o.Environment, true)
		if err != nil {
			return nil, err
		}
	}
	if _, err := url.ParseRequestURI(o.BaseURL); err != nil {
		return nil, err
	}
	return &SlackAPI{o.Token, o.BaseURL, o.HTTP}, nil
}
func (a *SlackAPI) request(ctx context.Context, method, endpoint, token string, body []byte, query string) (protocol.JSONObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	base, err := url.Parse(a.BaseURL)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	relative, err := url.Parse(endpoint)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	u := base.ResolveReference(relative)
	u.RawQuery = query
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	obj := object(protocol.DecodeJavaScriptUTF8(data))
	if string(obj.Get("ok")) != "true" {
		code := obj.String("error")
		if len(obj.Get("error")) == 0 || string(obj.Get("error")) == "null" {
			code = fmt.Sprintf("http_%d", res.StatusCode)
		}
		return obj, &SlackAPIError{endpoint, code, res.StatusCode}
	}
	return obj, nil
}
func (a *SlackAPI) Call(ctx context.Context, method string, body any, token string) (protocol.JSONObject, error) {
	if token == "" {
		token = a.Token
	}
	return a.request(ctx, "POST", method, token, raw(body), "")
}
func (a *SlackAPI) Get(ctx context.Context, method string, query [][2]string) (protocol.JSONObject, error) {
	parts := []string{}
	for _, v := range query {
		parts = append(parts, url.QueryEscape(v[0])+"="+url.QueryEscape(v[1]))
	}
	return a.request(ctx, "GET", method, a.Token, nil, strings.Join(parts, "&"))
}
func (a *SlackAPI) PostMessage(ctx context.Context, channel string, text Text, thread string) (string, error) {
	body := protocol.NewJSONObject(protocol.JSONField{Name: "channel", Value: raw(channel)}, protocol.JSONField{Name: "text", Value: raw(text)})
	if thread != "" {
		body.Set("thread_ts", raw(thread))
	}
	obj, err := a.Call(ctx, "chat.postMessage", body, "")
	if err != nil {
		return "", err
	}
	if !truthy(obj.Get("ts")) {
		return "", &SlackAPIError{Method: "chat.postMessage", Code: "missing_ts"}
	}
	return obj.String("ts"), nil
}
func (a *SlackAPI) UpdateMessage(ctx context.Context, channel, ts string, text Text) error {
	_, err := a.Call(ctx, "chat.update", protocol.NewJSONObject(protocol.JSONField{Name: "channel", Value: raw(channel)}, protocol.JSONField{Name: "ts", Value: raw(ts)}, protocol.JSONField{Name: "text", Value: raw(text)}), "")
	return err
}
func (a *SlackAPI) AuthTest(ctx context.Context) (protocol.JSONObject, error) {
	return a.Call(ctx, "auth.test", protocol.NewJSONObject(), "")
}
func (a *SlackAPI) OpenSocketConnection(ctx context.Context, appToken string) (string, error) {
	obj, err := a.Call(ctx, "apps.connections.open", protocol.NewJSONObject(), appToken)
	if err != nil {
		return "", err
	}
	if !truthy(obj.Get("url")) {
		return "", &SlackAPIError{Method: "apps.connections.open", Code: "missing_url"}
	}
	u, err := url.Parse(obj.String("url"))
	if err != nil {
		return "", err
	}
	if u.Scheme != "wss" {
		return "", &SlackAPIError{Method: "apps.connections.open", Code: "insecure_socket_url"}
	}
	return u.String(), nil
}
func AssertSlackFileURL(value string, upload bool) (string, error) {
	missing, bad := "Slack file has no download URL", "Slack file URL is not hosted by Slack"
	if upload {
		missing, bad = "Slack returned no upload URL", "Slack returned an unsafe upload URL"
	}
	if value == "" {
		return "", errors.New(missing)
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "files.slack.com") || u.Port() != "" && u.Port() != "443" || u.User != nil {
		return "", errors.New(bad)
	}
	u.Host = strings.ToLower(u.Host)
	if u.Port() == "443" {
		u.Host = u.Hostname()
	}
	if u.Path == "" {
		u.Path = "/"
	}
	u.Fragment = ""
	return u.String(), nil
}
func (a *SlackAPI) DownloadFile(ctx context.Context, address string) ([]byte, error) {
	address, err := AssertSlackFileURL(address, false)
	if err != nil {
		return nil, err
	}
	return a.fileRequest(ctx, "GET", address, nil, true, "download")
}
func (a *SlackAPI) fileRequest(ctx context.Context, method, address string, body []byte, auth bool, kind string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("Slack file %s failed: %d %s", kind, res.StatusCode, http.StatusText(res.StatusCode))
	}
	return io.ReadAll(res.Body)
}

type SlackUpload struct {
	Channel, Filename, Thread string
	Buffer                    []byte
	Title                     *string
}

func (a *SlackAPI) UploadFile(ctx context.Context, o SlackUpload) error {
	reserved, err := a.Get(ctx, "files.getUploadURLExternal", [][2]string{{"filename", o.Filename}, {"length", fmt.Sprint(len(o.Buffer))}})
	if err != nil {
		return err
	}
	address, err := AssertSlackFileURL(reserved.String("upload_url"), true)
	if err != nil {
		return err
	}
	id := reserved.String("file_id")
	if !truthy(reserved.Get("file_id")) {
		return &SlackAPIError{Method: "files.getUploadURLExternal", Code: "missing_file_id"}
	}
	if _, err = a.fileRequest(ctx, "POST", address, o.Buffer, false, "upload"); err != nil {
		return err
	}
	title := o.Filename
	if o.Title != nil {
		title = *o.Title
	}
	body := protocol.NewJSONObject(protocol.JSONField{Name: "files", Value: raw([]any{protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: raw(id)}, protocol.JSONField{Name: "title", Value: raw(title)})})}, protocol.JSONField{Name: "channel_id", Value: raw(o.Channel)})
	if o.Thread != "" {
		body.Set("thread_ts", raw(o.Thread))
	}
	_, err = a.Call(ctx, "files.completeUploadExternal", body, "")
	return err
}
func (a *SlackAPI) Close() { a.HTTP.CloseIdleConnections() }
