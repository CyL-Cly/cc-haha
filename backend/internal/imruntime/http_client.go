package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type HTTPClient struct {
	Config  Config
	Client  *http.Client
	BaseURL string
}

func NewHTTPClient(config Config, client *http.Client) *HTTPClient {
	if client == nil {
		client = &http.Client{}
	}
	base := strings.TrimSuffix(strings.Replace(strings.Replace(config.ServerURL, "ws:", "http:", 1), "wss:", "https:", 1), "/")
	roots := []string{}
	for _, root := range config.Roots {
		if resolved := existingDir(root, config.Home); resolved != "" {
			roots = append(roots, resolved)
		}
	}
	config.Roots = roots
	return &HTTPClient{config, client, base}
}
func (c *HTTPClient) Request(ctx context.Context, method, path string, body json.RawMessage) (protocol.JSONObject, int, error) {
	return c.request(ctx, method, path, body, true)
}
func (c *HTTPClient) request(ctx context.Context, method, path string, body json.RawMessage, parseSuccess bool) (protocol.JSONObject, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return protocol.JSONObject{}, 0, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	token := protocol.TrimJavaScriptString(c.Config.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"])
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.Client.Do(request)
	if err != nil {
		return protocol.JSONObject{}, 0, err
	}
	defer response.Body.Close()
	if !parseSuccess && response.StatusCode >= 200 && response.StatusCode < 300 {
		return protocol.NewJSONObject(), response.StatusCode, nil
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return httpStatusFallback(response), response.StatusCode, nil
		}
		return protocol.JSONObject{}, response.StatusCode, err
	}
	if (response.StatusCode < 200 || response.StatusCode >= 300) && strings.TrimSpace(string(raw)) == "null" {
		return protocol.JSONObject{}, response.StatusCode, fmt.Errorf("null is not an object (evaluating 'err.message')")
	}
	var data protocol.JSONObject
	if err = data.UnmarshalJSON(protocol.DecodeJavaScriptUTF8(raw)); err != nil {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			if json.Valid(protocol.DecodeJavaScriptUTF8(raw)) {
				return protocol.NewJSONObject(), response.StatusCode, nil
			}
			return httpStatusFallback(response), response.StatusCode, nil
		}
		return data, response.StatusCode, err
	}
	return data, response.StatusCode, nil
}
func httpStatusFallback(response *http.Response) protocol.JSONObject {
	statusText := http.StatusText(response.StatusCode)
	if _, text, found := strings.Cut(response.Status, " "); found {
		statusText = text
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: raw(statusText)})
}

func (c *HTTPClient) AllowedPath(path string) string {
	resolved := existingDir(path, c.Config.Home)
	if resolved == "" {
		return ""
	}
	for _, root := range c.Config.Roots {
		if within(resolved, root) {
			return resolved
		}
	}
	return ""
}
func (c *HTTPClient) CreateSession(ctx context.Context, work string) (string, error) {
	work = c.AllowedPath(work)
	if work == "" {
		return "", fmt.Errorf("Failed to create session: workDir is outside the configured project roots")
	}
	data, status, err := c.Request(ctx, "POST", "/api/sessions", raw(map[string]string{"workDir": work}))
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("Failed to create session: %s", httpErrorMessage(data))
	}
	return data.String("sessionId"), nil
}
func (c *HTTPClient) SessionExists(ctx context.Context, id string) (bool, error) {
	data, status, err := c.Request(ctx, "GET", "/api/sessions/"+url.PathEscape(id), nil)
	if status == 404 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status < 200 || status >= 300 {
		return false, fmt.Errorf("Failed to check session: %s", httpErrorMessage(data))
	}
	return data.String("permissionMode") != "bypassPermissions" && c.AllowedPath(data.String("workDir")) != "", nil
}
func (c *HTTPClient) Projects(ctx context.Context) ([]protocol.JSONObject, error) {
	data, status, err := c.Request(ctx, "GET", "/api/sessions/recent-projects", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Failed to list projects: %s", http.StatusText(status))
	}
	var projects []protocol.JSONObject
	_ = json.Unmarshal(data.Get("projects"), &projects)
	result := []protocol.JSONObject{}
	for _, project := range projects {
		path := project.String("realPath")
		if path == "" {
			path = project.String("projectPath")
		}
		if c.AllowedPath(path) != "" {
			result = append(result, project)
		}
	}
	return result, nil
}
func (c *HTTPClient) MatchProject(ctx context.Context, query string) (*protocol.JSONObject, []protocol.JSONObject, error) {
	if resolved := existingDir(query, c.Config.Home); resolved != "" {
		if c.AllowedPath(resolved) == "" {
			return nil, nil, nil
		}
		p := protocol.NewJSONObject()
		p.Set("realPath", raw(resolved))
		p.Set("projectPath", raw(resolved))
		name := filepath.Base(resolved)
		if name == string(filepath.Separator) {
			name = resolved
		}
		p.Set("projectName", raw(name))
		p.Set("branch", []byte("null"))
		return &p, nil, nil
	}
	projects, err := c.Projects(ctx)
	if err != nil {
		return nil, nil, err
	}
	query = protocol.TrimJavaScriptString(query)
	for i, project := range projects {
		if query == fmt.Sprint(i+1) {
			copy := project
			return &copy, nil, nil
		}
	}
	q := strings.ToLower(query)
	exact := []protocol.JSONObject{}
	for _, project := range projects {
		if strings.ToLower(project.String("projectName")) == q {
			exact = append(exact, project)
		}
	}
	if len(exact) == 1 {
		return &exact[0], nil, nil
	}
	if len(exact) > 1 {
		return nil, exact, nil
	}
	matches := []protocol.JSONObject{}
	for _, project := range projects {
		if strings.Contains(strings.ToLower(project.String("projectName")), q) || strings.Contains(strings.ToLower(project.String("realPath")), q) {
			matches = append(matches, project)
		}
	}
	if len(matches) == 1 {
		return &matches[0], nil, nil
	}
	return nil, matches, nil
}
