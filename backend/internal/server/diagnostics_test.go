package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
)

func TestDiagnosticsHTTPAgainstActualSource(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "go-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)
	service, err := diagnosticsapi.New(diagnosticsapi.Options{HomeDir: home, ConfigDir: filepath.Join(home, ".claude"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	type action struct {
		Action string  `json:"action"`
		Method string  `json:"method"`
		Path   string  `json:"path"`
		Body   *string `json:"body,omitempty"`
	}
	body := func(s string) *string { return &s }
	rows := []action{
		{"api", "GET", "/api/diagnostics/events", nil},
		{"api", "POST", "/api/diagnostics/events", body(`{"type":" custom ","severity":"error","summary":"  secret token=fake ","details":{"errorCode":"CLI_EXITED","output":"private","apiKey":"fake"}}`)},
		{"api", "POST", "/api/diagnostics/events", body(`{"type":" ","severity":"fatal","summary":null,"sessionId":17}`)},
		{"api", "POST", "/api/diagnostics/events", body(`null`)},
		{"api", "POST", "/api/diagnostics/events", body(`[]`)},
		{"api", "POST", "/api/diagnostics/events", body(`true`)},
		{"api", "GET", "/api/diagnostics/events?limit=0", nil},
		{"api", "GET", "/api/diagnostics/events?limit=2junk", nil},
		{"api", "GET", "/api/diagnostics/events?limit=bad", nil},
		{"api", "GET", "/api/diagnostics/events?limit=-1", nil},
		{"api", "GET", "/api/diagnostics/events?limit=1e3", nil},
		{"api", "POST", "/api/diagnostics/events", body(`{"type":"bad"} trailing`)},
		{"api", "GET", "/api/diagnostics/unknown", nil},
		{"api", "POST", "/api/diagnostics/status", body(`{`)},
		{"api", "DELETE", "/api/diagnostics", nil},
		{"api", "GET", "/api/diagnostics/events", nil},
		{"api", "GET", "/api/diagnostics", nil},
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failure := (&Server{}).diagnosticsHTTP(w, r, strings.Split(strings.Trim(r.URL.Path, "/"), "/"), service)
		if failure != nil {
			var api *apiError
			if errors.As(failure, &api) {
				writeJSON(w, api.status, map[string]string{"error": api.code, "message": api.message})
			} else {
				writeJSON(w, 500, map[string]string{"error": "INTERNAL_ERROR", "message": "An unexpected error occurred"})
			}
		}
	}))
	defer host.Close()
	got := []any{}
	for _, row := range rows {
		var data io.Reader
		if row.Body != nil {
			data = strings.NewReader(*row.Body)
		}
		request, _ := http.NewRequest(row.Method, host.URL+row.Path, data)
		response, err := host.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err = json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		got = append(got, map[string]any{"status": response.StatusCode, "body": value})
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	sourceHome := filepath.Join(root, "source-home")
	for _, path := range []string{sourceHome, filepath.Join(sourceHome, ".claude"), filepath.Join(sourceHome, "tmp")} {
		if err = os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-diagnostics.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = sourceHome
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_DESKTOP_DIAGNOSTICS_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"now": now.Format(time.RFC3339Nano), "cases": rows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Source diagnostics API %v %s", err, stderr.String())
	}
	var want []any
	if err = json.Unmarshal(output, &want); err != nil {
		t.Fatalf("Source %s %v", output, err)
	}
	normalize := func(value any) any {
		raw, _ := json.Marshal(value)
		text := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`).ReplaceAllString(string(raw), "<event-id>")
		var result any
		_ = json.Unmarshal([]byte(text), &result)
		return result
	}
	if len(got) != len(want) {
		t.Fatal("observation count", len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(normalize(got[i]), normalize(want[i])) {
			g, _ := json.Marshal(got[i])
			w, _ := json.Marshal(want[i])
			t.Fatalf("HTTP case %d %s %s\nGo %s\nSource %s", i, rows[i].Method, rows[i].Path, g, w)
		}
	}
}

func TestDiagnosticsNativeOpenAndLocalIndexBoundaries(t *testing.T) {
	home := t.TempDir()
	var opened string
	rebuilds := 0
	service, err := diagnosticsapi.New(diagnosticsapi.Options{HomeDir: home, ConfigDir: filepath.Join(home, ".claude"), OpenDir: func(_ context.Context, path string) error { opened = path; return nil }, LocalIndexStatus: func() (any, error) { return map[string]any{"state": "ready", "indexed": 3}, nil }, RebuildLocalIndex: func(context.Context) (any, error) {
		rebuilds++
		return map[string]any{"state": "building", "indexed": 0}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ method, path, body string }{{"POST", "/api/diagnostics/open-log-dir", ""}, {"GET", "/api/diagnostics/local-index", ""}, {"POST", "/api/diagnostics/local-index/rebuild", `{"path":"/protected/must-not-use.sqlite"}`}} {
		request := httptest.NewRequest(row.method, row.path, strings.NewReader(row.body))
		writer := httptest.NewRecorder()
		if err = (&Server{}).diagnosticsHTTP(writer, request, strings.Split(strings.Trim(row.path, "/"), "/"), service); err != nil || writer.Code != 200 {
			t.Fatalf("%s %v %s", row.path, err, writer.Body.String())
		}
	}
	if opened != service.LogDir() || rebuilds != 1 {
		t.Fatalf("native boundaries %q %d", opened, rebuilds)
	}
	unimplemented, _ := diagnosticsapi.New(diagnosticsapi.Options{HomeDir: home, ConfigDir: filepath.Join(home, ".claude")})
	writer := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/diagnostics/local-index", nil)
	err = (&Server{}).diagnosticsHTTP(writer, request, []string{"api", "diagnostics", "local-index"}, unimplemented)
	var api *apiError
	if !errors.As(err, &api) || api.status != 501 {
		t.Fatalf("fake index success: %v", err)
	}
}
