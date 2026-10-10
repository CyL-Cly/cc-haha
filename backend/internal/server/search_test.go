package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/searchapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func TestSearchHTTPActualSourceValidationAndCanonicalBoundary(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun unavailable")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	config := filepath.Join(home, ".claude")
	workspace := filepath.Join(home, "workspace")
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(workspace, 0700)
	content, e := localindex.NewContentCoordinator(localindex.ContentCoordinatorOptions{Scope: config, Budget: sessionstore.NewBudget(), SpoolDir: filepath.Join(home, "tmp")})
	if e != nil {
		t.Fatal(e)
	}
	service, e := searchapi.New(searchapi.Options{ConfigDir: config, WorkDir: workspace, Environment: map[string]string{"PATH": "", "HOME": home}, Content: content, Metadata: func(context.Context, []string) (map[string]searchapi.Metadata, error) { return nil, nil }, ReadEntriesAtLines: func(context.Context, string, map[int64]bool) (*searchapi.TargetedEntries, error) { return nil, nil }, ParseDate: func(s string) (float64, bool, error) { return sessionprojection.ParseDateUTC(utf16.Encode([]rune(s))) }, Ripgrep: func() searchapi.Command { return searchapi.Command{} }})
	if e != nil {
		t.Fatal(e)
	}
	cases := []map[string]any{{"kind": "api", "method": "GET", "body": ""}, {"kind": "api", "method": "PUT", "body": "{}"}, {"kind": "api", "method": "POST", "body": "{"}, {"kind": "api", "method": "POST", "body": "null"}, {"kind": "api", "method": "POST", "body": "[]"}, {"kind": "api", "method": "POST", "body": "true"}, {"kind": "api", "method": "POST", "body": "{}"}, {"kind": "api", "method": "POST", "body": `{"query":0}`}, {"kind": "api", "method": "POST", "body": `{"query":""}`}, {"kind": "api", "method": "POST", "sub": "unknown", "body": `{"query":"hello"}`}, {"kind": "api", "method": "POST", "sub": "sessions", "body": `{"query":"  "}`}, {"kind": "api", "method": "POST", "sub": "sessions", "body": `{"query":"hello","project":"../escape"}`}, {"kind": "api", "method": "POST", "sub": "sessions", "body": `{"query":"hello"}`}, {"kind": "api", "method": "POST", "body": `{"query":"hello","cwd":"$WORKSPACE"}`}, {"kind": "api", "method": "POST", "sub": "sessions", "body": `{"query":"hello"}`, "aborted": true}}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e := (&Server{}).searchHTTP(w, r, segments(r.URL.Path), service); e != nil {
			writeError(w, e)
		}
	}))
	defer host.Close()
	observed := []any{}
	for _, row := range cases {
		body := strings.ReplaceAll(row["body"].(string), "$WORKSPACE", workspace)
		sub, _ := row["sub"].(string)
		path := "/api/search"
		if sub != "" {
			path += "/" + sub
		}
		if row["aborted"] == true {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest(row["method"].(string), path, strings.NewReader(body)).WithContext(ctx)
			writer := httptest.NewRecorder()
			if e := (&Server{}).searchHTTP(writer, req, segments(path), service); e != nil {
				writeError(writer, e)
			}
			observed = append(observed, map[string]any{"status": writer.Code, "body": nil})
			continue
		}
		req, _ := http.NewRequest(row["method"].(string), host.URL+path, strings.NewReader(body))
		response, e := host.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		var value any
		_ = json.NewDecoder(response.Body).Decode(&value)
		_ = response.Body.Close()
		observed = append(observed, map[string]any{"status": response.StatusCode, "body": value})
	}
	sourceHome := filepath.Join(allocation, "source-home")
	_ = os.MkdirAll(filepath.Join(sourceHome, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-search.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_SEARCH_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"cases": cases})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("Actual Source HTTP %v %s", e, stderr.String())
	}
	var expected struct{ Observations []any }
	if e = json.Unmarshal(data, &expected); e != nil {
		t.Fatal(e)
	}
	normalized, _ := json.Marshal(observed)
	var actual []any
	_ = json.Unmarshal(normalized, &actual)
	if !reflect.DeepEqual(actual, expected.Observations) {
		t.Fatalf("HTTP Search differs native=%s Source=%s", normalized, data)
	}
}
