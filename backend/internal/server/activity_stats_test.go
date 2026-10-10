package server

import (
	"bytes"
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
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
)

func TestActivityHTTPAgainstActualSource(t *testing.T) {
	allocation := t.TempDir()
	home := filepath.Join(allocation, "go-home")
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(config, "projects", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 12, 34, 56, 789000000, time.UTC)
	entries := []any{map[string]any{"type": "user", "timestamp": "2026-10-05T23:59:00Z"}, map[string]any{"type": "assistant", "timestamp": "2026-10-06T00:01:00Z", "message": map[string]any{"model": "deepseek-flash", "usage": map[string]any{"input_tokens": 100, "output_tokens": 20}, "content": []any{map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{}}}}}}
	var transcript bytes.Buffer
	for _, entry := range entries {
		raw, _ := json.Marshal(entry)
		transcript.Write(raw)
		transcript.WriteByte('\n')
	}
	path := filepath.Join(config, "projects", "private", "branch.jsonl")
	if err := os.WriteFile(path, transcript.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	service, err := activitystatsapi.New(activitystatsapi.Options{HomeDir: home, ConfigDir: config, Now: func() time.Time { return now }, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"api": true, "method": "GET", "path": "/api/activity-stats"},
		{"api": true, "method": "GET", "path": "/api/activity-stats/all"},
		{"api": true, "method": "GET", "path": "/api/activity-stats/7d"},
		{"api": true, "method": "GET", "path": "/api/activity-stats/30d"},
		{"api": true, "method": "GET", "path": "/api/activity-stats/7d/ignored?from=1900-01-01"},
		{"api": true, "method": "GET", "path": "/api/activity-stats/bad"},
		{"api": true, "method": "POST", "path": "/api/activity-stats"},
		{"api": true, "method": "DELETE", "path": "/api/activity-stats/bad"},
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := (&Server{}).activityStatsHTTP(w, r, strings.Split(strings.Trim(r.URL.Path, "/"), "/"), service)
		if err != nil {
			var api *apiError
			if errors.As(err, &api) {
				writeJSON(w, api.status, map[string]string{"error": api.code, "message": api.message})
			} else {
				writeJSON(w, 500, map[string]string{"error": "INTERNAL_ERROR", "message": "An unexpected error occurred"})
			}
		}
	}))
	defer host.Close()
	got := []any{}
	for i, row := range rows {
		request, _ := http.NewRequest(row["method"].(string), host.URL+row["path"].(string), nil)
		response, err := host.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var body any
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		got = append(got, map[string]any{"status": float64(response.StatusCode), "body": body})
		row["continue"] = i > 0
		if i == 0 {
			row["files"] = []any{map[string]any{"path": "private/branch.jsonl", "entries": entries}}
		}
	}
	sourceHome := filepath.Join(allocation, "source-home")
	for _, path := range []string{sourceHome, filepath.Join(sourceHome, ".claude"), filepath.Join(sourceHome, "tmp")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-activity-stats.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = sourceHome
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "TZ=UTC", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_ACTIVITY_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"now": now.Format(time.RFC3339Nano), "cases": rows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Actual Source API %v %s", err, stderr.String())
	}
	var want []map[string]any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("Source API %v %s", err, output)
	}
	for i, row := range want {
		delete(row, "cache")
		if !reflect.DeepEqual(row, got[i]) {
			a, _ := json.Marshal(row)
			b, _ := json.Marshal(got[i])
			t.Fatalf("case%d\nSource %s\nGo %s", i, a, b)
		}
	}
}

func TestActivityHTTPReportsCorruptTranscript(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	directory := filepath.Join(config, "projects", "private")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bad.jsonl"), []byte("null\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service, _ := activitystatsapi.New(activitystatsapi.Options{HomeDir: home, ConfigDir: config})
	request := httptest.NewRequest("GET", "/api/activity-stats", nil)
	response := httptest.NewRecorder()
	if err := (&Server{}).activityStatsHTTP(response, request, []string{"api", "activity-stats"}, service); err == nil {
		t.Fatal("Invalid record should reach real error boundary, not return an empty success")
	}
}
