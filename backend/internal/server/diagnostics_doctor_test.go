package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/doctorapi"
)

func TestDoctorHTTPAgainstActualSource(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "go-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	now := time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)
	files := map[string]string{"settings.json": "{}", ".claude.json": "{}", "cc-haha/providers.json": `{"activeId":null,"providers":[]}`, "projects/test/session.jsonl": "{}\n{broken\n"}
	for relative, content := range files {
		path := filepath.Join(config, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	service, err := doctorapi.New(doctorapi.Options{HomeDir: home, ConfigDir: config, UsesConfigDirOverride: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	body := func(text string) *string { return &text }
	type row struct {
		Action string  `json:"action"`
		Method string  `json:"method"`
		Path   string  `json:"path"`
		Body   *string `json:"body,omitempty"`
	}
	rows := []row{
		{"doctorApi", "GET", "/api/doctor", nil},
		{"doctorApi", "GET", "/api/doctor/report?cwd=<home>/project", nil},
		{"doctorApi", "GET", "/api/doctor/report/extra", nil},
		{"doctorApi", "POST", "/api/doctor/repair", nil},
		{"doctorApi", "POST", "/api/doctor/repair", body(`{"targetIds":[null,12,"","user-mcp","not-found"]}`)},
		{"doctorApi", "POST", "/api/doctor/repair", body(`{"targetIds":[],"cwd":"<home>/project"}`)},
		{"doctorApi", "POST", "/api/doctor/repair?cwd=<home>/project", body(`{"cwd":"","targetIds":["project-settings"]}`)},
		{"doctorApi", "POST", "/api/doctor/repair", body(`null`)},
		{"doctorApi", "POST", "/api/doctor/repair", body(`[]`)},
		{"doctorApi", "POST", "/api/doctor/repair", body(`{"targetIds":[]`)},
		{"doctorApi", "DELETE", "/api/doctor", nil},
		{"doctorApi", "POST", "/api/doctor/report", nil},
		{"doctorApi", "GET", "/api/doctor/missing", nil},
	}
	observedHeaders := make(chan map[string]string, 1)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := map[string]string{}
		for _, key := range []string{"Content-Length", "Content-Type", "Transfer-Encoding"} {
			if value := r.Header.Get(key); value != "" {
				headers[key] = value
			}
		}
		if len(r.TransferEncoding) > 0 {
			headers["Transfer-Encoding"] = strings.Join(r.TransferEncoding, ", ")
		}
		observedHeaders <- headers
		if failure := (&Server{}).doctorHTTP(w, r, strings.Split(strings.Trim(r.URL.Path, "/"), "/"), service); failure != nil {
			writeError(w, failure)
		}
	}))
	defer host.Close()
	got := []any{}
	cases := []map[string]any{}
	for _, row := range rows {
		var data *strings.Reader
		if row.Body != nil {
			data = strings.NewReader(strings.ReplaceAll(*row.Body, "<home>", home))
		} else {
			data = strings.NewReader("")
		}
		request, _ := http.NewRequest(row.Method, host.URL+strings.ReplaceAll(row.Path, "<home>", home), data)
		if row.Body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
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
		original := map[string]any{"action": row.Action, "method": row.Method, "path": row.Path, "headers": <-observedHeaders}
		if row.Body != nil {
			original["body"] = *row.Body
		}
		cases = append(cases, original)
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
	input, _ := json.Marshal(map[string]any{"now": now.Format(time.RFC3339Nano), "files": files, "cases": cases})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Source Doctor HTTP %v %s", err, stderr.String())
	}
	var want []any
	if err = json.Unmarshal(output, &want); err != nil {
		t.Fatalf("Source Doctor output %s %v", output, err)
	}
	if len(want) != len(got) {
		t.Fatal("observations", len(got), len(want))
	}
	for i := range got {
		encoded, _ := json.Marshal(got[i])
		var normalized any
		_ = json.Unmarshal(encoded, &normalized)
		if !reflect.DeepEqual(normalized, want[i]) {
			g, _ := json.MarshalIndent(got[i], "", "  ")
			w, _ := json.MarshalIndent(want[i], "", "  ")
			t.Fatalf("HTTP case %d %s %s\nGo %s\nSource %s", i, rows[i].Method, rows[i].Path, g, w)
		}
	}
}

func TestDoctorBodylessRepairHeaderContract(t *testing.T) {
	home := t.TempDir()
	service, err := doctorapi.New(doctorapi.Options{HomeDir: home, ConfigDir: filepath.Join(home, ".claude")})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		header, value string
		bad           bool
	}{{"", "", false}, {"Content-Length", "0", true}, {"Content-Type", "application/json", true}, {"Transfer-Encoding", "chunked", true}} {
		request := httptest.NewRequest("POST", "/api/doctor/repair", nil)
		if row.header != "" {
			request.Header.Set(row.header, row.value)
		}
		writer := httptest.NewRecorder()
		failure := (&Server{}).doctorHTTP(writer, request, []string{"api", "doctor", "repair"}, service)
		if row.bad && failure == nil {
			t.Fatalf("empty JSON body with %s accepted", row.header)
		}
		if !row.bad && (failure != nil || writer.Code != 200) {
			t.Fatalf("no body headers: %v %s", failure, writer.Body.String())
		}
	}
}
