package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopMemoryHTTPUsesActualUserFilesAndRevisionState(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	o.WorkDir = filepath.Join(home, "work")
	if err := os.MkdirAll(o.WorkDir, 0700); err != nil {
		t.Fatal(err)
	}
	projectID := projectpath.Sanitize(o.WorkDir)
	memory := filepath.Join(config, "projects", projectID, "memory")
	if err := os.MkdirAll(memory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memory, "MEMORY.md"), []byte("Initial index."), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	host := httptest.NewServer(service)
	t.Cleanup(host.Close)
	request := func(method, path string, body any, status int) map[string]any {
		t.Helper()
		var input []byte
		if body != nil {
			input, _ = json.Marshal(body)
		}
		req, err := http.NewRequest(method, host.URL+path, bytes.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s %s: %d %s %v", method, path, response.StatusCode, data, err)
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	projects := request("GET", "/api/memory/projects?cwd="+url.QueryEscape(o.WorkDir), nil, 200)
	items := projects["projects"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != projectID || items[0].(map[string]any)["isCurrent"] != true {
		t.Fatalf("actual project not visible: %+v", projects)
	}
	query := "?projectId=" + url.QueryEscape(projectID)
	read := request("GET", "/api/memory/file"+query+"&path=MEMORY.md", nil, 200)["file"].(map[string]any)
	input := map[string]any{"projectId": projectID, "path": "MEMORY.md", "content": "Updated through Go HTTP.", "expectedUpdatedAt": read["updatedAt"], "expectedBytes": read["bytes"]}
	request("PUT", "/api/memory/file", input, 200)
	actual, err := os.ReadFile(filepath.Join(memory, "MEMORY.md"))
	if err != nil || string(actual) != input["content"] {
		t.Fatal("memory save did not reach actual original file", string(actual), err)
	}
	request("PUT", "/api/memory/file", input, 409)
	request("PUT", "/api/memory/file", map[string]any{"projectId": projectID, "path": "notes/facts.md", "content": "---\ntype: project\ndescription: Saved note\n---\nFacts."}, 200)
	files := request("GET", "/api/memory/files"+query, nil, 200)["files"].([]any)
	if len(files) != 2 || files[0].(map[string]any)["isIndex"] != true || files[1].(map[string]any)["path"] != "notes/facts.md" {
		t.Fatal("actual saved files not visible", files)
	}
	request("GET", "/api/memory/file"+query+"&path=../settings.md", nil, 400)
	request("POST", "/api/memory/projects", nil, 405)
	request("GET", "/api/memory/unknown", nil, 404)
}
