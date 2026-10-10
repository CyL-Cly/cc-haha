package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestAgentsHTTPInputErrorsAgainstActualSource(t *testing.T) {
	allocation := t.TempDir()
	home := filepath.Join(allocation, "go-home")
	config := filepath.Join(home, ".claude")
	workspace := filepath.Join(home, "workspace")
	for _, path := range []string{config, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	notReached := func() error {
		t.Error("Invalid input reached an actual producer before validation")
		return errors.New("invalid input passed validation")
	}
	service, err := agentsapi.New(agentsapi.Options{HomeDir: home, ConfigDir: config, WorkDir: workspace, Load: func(context.Context, string) (*agentcatalog.Result, error) { return nil, notReached() }, AvailableTools: func(context.Context, string) ([]string, error) { return nil, notReached() }, Invalidate: func(context.Context) error { return notReached() }, Locked: func(context.Context) (bool, error) { return false, notReached() }, UpdateOverride: func(context.Context, string, storage.Object) error { return notReached() }, Reload: func(context.Context, string) (protocol.JSONObject, error) { return protocol.JSONObject{}, notReached() }})
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"action": "api", "method": "POST", "path": "/api/agents", "raw": "{"},
		{"action": "api", "method": "POST", "path": "/api/agents", "raw": "null"},
		{"action": "api", "method": "POST", "path": "/api/agents", "raw": "[]"},
		{"action": "api", "method": "POST", "path": "/api/agents", "raw": "{}"},
		{"action": "api", "method": "POST", "path": "/api/agents", "raw": "{\"name\":\"Upper\",\"description\":\"d\",\"systemPrompt\":\"s\"}"},
		{"action": "api", "method": "POST", "path": "/api/agents", "raw": "{\"name\":\"ok\",\"description\":\"d\",\"systemPrompt\":\"s\",\"tools\":[false]}"},
		{"action": "api", "method": "PUT", "path": "/api/agents/ok", "raw": "{\"systemPrompt\":null}"},
		{"action": "api", "method": "PUT", "path": "/api/agents/Explore/override", "raw": "{\"scope\":\"user\"}"},
		{"action": "api", "method": "GET", "path": "/api/agents/Explore"},
		{"action": "api", "method": "GET", "path": "/api/agents/%FF"},
		{"action": "api", "method": "DELETE", "path": "/api/agents/missing?scope=invalid"},
		{"action": "api", "method": "POST", "path": "/api/agents/reload"},
		{"action": "api", "method": "PATCH", "path": "/api/agents"},
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failure := (&Server{}).agentsHTTP(w, r, segments(r.URL.EscapedPath()), service)
		if failure != nil {
			writeError(w, failure)
		}
	}))
	defer host.Close()
	observed := []any{}
	for _, row := range rows {
		body := ""
		if value, ok := row["raw"].(string); ok {
			body = value
		}
		request, _ := http.NewRequest(row["method"].(string), host.URL+row["path"].(string), strings.NewReader(body))
		response, err := host.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err = json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		observed = append(observed, map[string]any{"status": float64(response.StatusCode), "body": value})
	}
	sourceHome := filepath.Join(allocation, "source-home")
	for _, path := range []string{sourceHome, filepath.Join(sourceHome, ".claude"), filepath.Join(sourceHome, "tmp")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agents-api.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = sourceHome
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_AGENTS_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"cases": rows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Actual Source agent HTTP %v %s", err, stderr.String())
	}
	var expected []map[string]any
	if err = json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, row := range expected {
		delete(row, "files")
		delete(row, "settings")
		if !reflect.DeepEqual(row, observed[i]) {
			a, _ := json.Marshal(row)
			b, _ := json.Marshal(observed[i])
			t.Fatalf("case%d Source %s Go %s", i, a, b)
		}
	}
}
