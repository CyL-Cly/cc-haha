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

	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidermodels"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestProviderModelsUnsavedHTTPAgainstActualSource(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-provider-key-123456" || r.Header.Get("Accept") != "application/json" {
			t.Error("catalog headers invalid")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"flash","owned_by":"deepseek"},"flash","small"]}`))
	}))
	defer peer.Close()
	options := nativeprovidermodels.Options{Environment: func() map[string]string { return map[string]string{} }, Settings: func(context.Context) (protocol.JSONObject, error) {
		var settings protocol.JSONObject
		_ = json.Unmarshal([]byte(`{"network":{"proxy":{"mode":"direct"}}}`), &settings)
		return settings, nil
	}}
	service, err := nativeprovidermodels.New(options)
	if err != nil {
		t.Fatal(err)
	}
	body := func(value any) string { raw, _ := json.Marshal(value); return string(raw) }
	rows := []string{
		`{`, `null`, `[]`, `true`, `12`, `{}`, `{"baseUrl":""}`, `{"baseUrl":null,"apiKey":12,"isFullUrl":"true"}`,
		`{"baseUrl":" ","apiKey":"fake"}`, `{"baseUrl":"https://api.example.com","apiKey":" "}`, `{"baseUrl":"file:///etc/hosts","apiKey":"fake"}`,
		body(map[string]any{"baseUrl": peer.URL, "apiKey": "fake-provider-key-123456", "modelsUrl": "file:///protected/ignored.json", "unknown": "ignored"}),
		body(map[string]any{"baseUrl": peer.URL, "apiKey": "fake-provider-key-123456", "isFullUrl": nil}),
		body(map[string]any{"baseUrl": peer.URL + "/v1/chat/completions", "apiKey": "fake-provider-key-123456", "isFullUrl": true}),
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := (&Server{}).providerModelsHTTP(w, r, service); err != nil {
			writeError(w, err)
		}
	}))
	defer host.Close()
	got := []any{}
	cases := []any{}
	for _, row := range rows {
		response, err := host.Client().Post(host.URL+"/api/providers/models", "application/json", strings.NewReader(row))
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err = json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		got = append(got, map[string]any{"status": float64(response.StatusCode), "body": value})
		cases = append(cases, map[string]any{"action": "api", "body": row})
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err = os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-provider-model-catalog.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = home
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_PROVIDER_MODELS_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"cases": cases})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Source provider models API %v %s", err, stderr.String())
	}
	var want []any
	if err = json.Unmarshal(output, &want); err != nil {
		t.Fatalf("Source output %s %v", output, err)
	}
	if len(got) != len(want) {
		t.Fatal("observations", len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			g, _ := json.Marshal(got[i])
			w, _ := json.Marshal(want[i])
			t.Fatalf("unsaved models HTTP case %d\nGo %s\nSource %s", i, g, w)
		}
	}
}
