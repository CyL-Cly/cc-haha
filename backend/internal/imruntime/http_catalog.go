package imruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func EncodeURIComponent(value string) string {
	var result strings.Builder
	for _, b := range []byte(value) {
		if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || strings.ContainsRune("-_.!~*'()", rune(b)) {
			result.WriteByte(b)
		} else {
			fmt.Fprintf(&result, "%%%02X", b)
		}
	}
	return result.String()
}
func (c *HTTPClient) Checked(ctx context.Context, method, path string, body json.RawMessage, label string) (protocol.JSONObject, error) {
	data, status, err := c.Request(ctx, method, path, body)
	if err != nil {
		return data, err
	}
	if status < 200 || status >= 300 {
		return data, fmt.Errorf("%s: %s", label, data.String("message"))
	}
	return data, nil
}
func (c *HTTPClient) Change(ctx context.Context, method, path string, body json.RawMessage) error {
	data, status, err := c.Request(ctx, method, path, body)
	if status >= 200 && status < 300 && (err == nil || err == io.EOF || err == io.ErrUnexpectedEOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("Request failed: %s", data.String("message"))
}
func (c *HTTPClient) Git(ctx context.Context, id string) (protocol.JSONObject, error) {
	return c.Checked(ctx, "GET", "/api/sessions/"+EncodeURIComponent(id)+"/git-info", nil, "Failed to load git info")
}
func (c *HTTPClient) Tasks(ctx context.Context, id string) ([]protocol.JSONObject, error) {
	data, status, err := c.Request(ctx, "GET", "/api/tasks/lists/"+EncodeURIComponent(id), nil)
	if status == 404 {
		return []protocol.JSONObject{}, nil
	}
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Failed to load tasks: %s", data.String("message"))
	}
	return collectJSONArray(data.Get("tasks")), nil
}
func (c *HTTPClient) Sessions(ctx context.Context, project string, limit, offset int) ([]protocol.JSONObject, int, error) {
	if project != "" && c.AllowedPath(project) == "" {
		return []protocol.JSONObject{}, 0, nil
	}
	params := url.Values{}
	if project != "" {
		params.Set("project", project)
	}
	params.Set("limit", stringNumber(limit))
	params.Set("offset", stringNumber(offset))
	data, err := c.Checked(ctx, "GET", "/api/sessions?"+params.Encode(), nil, "Failed to list sessions")
	if err != nil {
		return nil, 0, err
	}
	var total int
	_ = json.Unmarshal(data.Get("total"), &total)
	result := []protocol.JSONObject{}
	for _, session := range collectJSONArray(data.Get("sessions")) {
		if session.String("permissionMode") != "bypassPermissions" && c.AllowedPath(session.String("workDir")) != "" {
			result = append(result, session)
		}
	}
	return result, total, nil
}
func (c *HTTPClient) SessionHistory(ctx context.Context) ([]protocol.JSONObject, error) {
	result := []protocol.JSONObject{}
	indexes := map[string]int{}
	for offset := 0; ; offset += 100 {
		page, total, err := c.Sessions(ctx, "", 100, offset)
		if err != nil {
			return nil, err
		}
		for _, session := range page {
			id := session.String("id")
			if old, ok := indexes[id]; ok {
				result[old] = session
			} else {
				indexes[id] = len(result)
				result = append(result, session)
			}
		}
		if offset+100 >= total {
			break
		}
	}
	return result, nil
}
func (c *HTTPClient) ProjectHistory(ctx context.Context, project string, all []protocol.JSONObject) ([]protocol.JSONObject, error) {
	if all == nil {
		var err error
		all, err = c.SessionHistory(ctx)
		if err != nil {
			return nil, err
		}
	}
	root := canonicalPath(project)
	result := []protocol.JSONObject{}
	for _, session := range all {
		work := session.String("workDir")
		projectRoot := session.String("projectRoot")
		if projectRoot == "" {
			projectRoot = work
		}
		if work != "" && string(session.Get("workDirExists")) != "false" && (canonicalPath(projectRoot) == root || canonicalPath(work) == root) {
			result = append(result, session)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.String("modifiedAt") != b.String("modifiedAt") {
			return a.String("modifiedAt") > b.String("modifiedAt")
		}
		return a.String("id") < b.String("id")
	})
	return result, nil
}
func (c *HTTPClient) Providers(ctx context.Context) (protocol.JSONObject, error) {
	return c.Checked(ctx, "GET", "/api/providers", nil, "Failed to list providers")
}
func (c *HTTPClient) Activate(ctx context.Context, id string) error {
	path := "/api/providers/" + EncodeURIComponent(id) + "/activate"
	if id == "official" {
		path = "/api/providers/official"
	}
	return c.Change(ctx, "POST", path, nil)
}
func (c *HTTPClient) Models(ctx context.Context) (protocol.JSONObject, error) {
	return c.Checked(ctx, "GET", "/api/models", nil, "Failed to list models")
}
func (c *HTTPClient) CurrentModel(ctx context.Context) (protocol.JSONObject, error) {
	return c.Checked(ctx, "GET", "/api/models/current", nil, "Failed to get current model")
}
func (c *HTTPClient) SetModel(ctx context.Context, id string) error {
	return c.Change(ctx, "PUT", "/api/models/current", raw(map[string]string{"modelId": id}))
}
func (c *HTTPClient) Skills(ctx context.Context, cwd string) (protocol.JSONObject, error) {
	cwd = c.AllowedPath(cwd)
	if cwd == "" {
		return protocol.JSONObject{}, fmt.Errorf("Failed to list skills: cwd is outside the configured project roots")
	}
	return c.Checked(ctx, "GET", "/api/skills?cwd="+url.QueryEscape(cwd), nil, "Failed to list skills")
}
