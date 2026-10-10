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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidertest"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestProviderConnectionHTTPAndSavedKeyBoundaryAgainstSource(t *testing.T) {
	var requests, lookups atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body protocol.JSONObject
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"type":"message","model":"` + body.String("model") + `","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer peer.Close()
	now := time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)
	saved := map[string]any{"id": "saved", "presetId": "custom", "name": "Private saved", "baseUrl": peer.URL, "apiKey": "fake-saved-key-123456", "apiFormat": "anthropic", "models": map[string]string{"main": "flash", "haiku": "flash", "sonnet": "flash", "opus": "flash"}}
	rawSaved, _ := json.Marshal(saved)
	var record protocol.JSONObject
	_ = json.Unmarshal(rawSaved, &record)
	service, err := nativeprovidertest.New(nativeprovidertest.Options{Version: "fixture-version", Now: func() time.Time { return now }, Environment: func() map[string]string { return map[string]string{} }, Settings: func(context.Context) (protocol.JSONObject, error) {
		var o protocol.JSONObject
		_ = json.Unmarshal([]byte(`{"network":{"proxy":{"mode":"direct"}}}`), &o)
		return o, nil
	}, Provider: func(_ context.Context, id string) (protocol.JSONObject, error) {
		lookups.Add(1)
		if id != "saved" {
			return protocol.JSONObject{}, notFound("Provider not found: " + id)
		}
		return record, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	encode := func(value any) string { raw, _ := json.Marshal(value); return string(raw) }
	type row struct{ path, body string }
	rows := []row{
		{"/api/providers/test", `{`}, {"/api/providers/test", `null`}, {"/api/providers/test", `{}`}, {"/api/providers/test", encode(map[string]any{"baseUrl": peer.URL, "apiKey": "fake-saved-key-123456", "modelId": "flash[1m]", "authStrategy": "auth_token"})},
		{"/api/providers/saved/test", `{`}, {"/api/providers/saved/test", `{}`}, {"/api/providers/saved/test", `{"modelId":"other[1m]"}`}, {"/api/providers/saved/test", `{"modelId":""}`}, {"/api/providers/saved/test", `{"apiKey":"ignored-key"}`},
		{"/api/providers/saved/test", `{"baseUrl":"http://must-not-redirect.invalid"}`}, {"/api/providers/saved/test", `{"baseUrl":null}`}, {"/api/providers/saved/test", `{"apiFormat":"openai_chat"}`}, {"/api/providers/saved/test", `{"authStrategy":"dual_dummy"}`}, {"/api/providers/saved/test", `{"modelId":null}`}, {"/api/providers/missing/test", `{}`},
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		id := ""
		if len(parts) > 3 {
			id = parts[2]
		}
		if failure := (&Server{}).providerConnectionTestHTTP(w, r, id, service); failure != nil {
			writeError(w, failure)
		}
	}))
	defer host.Close()
	got := []any{}
	cases := []any{}
	for _, row := range rows {
		before := lookups.Load()
		response, err := host.Client().Post(host.URL+row.path, "application/json", strings.NewReader(row.body))
		if err != nil {
			t.Fatal(err)
		}
		var body any
		if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		got = append(got, map[string]any{"status": float64(response.StatusCode), "body": body})
		cases = append(cases, map[string]any{"action": "api", "path": row.path, "body": row.body, "saved": saved})
		if strings.Contains(row.body, "cannot-redirect") || strings.Contains(row.body, "must-not-redirect") || strings.Contains(row.body, `"baseUrl":null`) || strings.Contains(row.body, `"apiFormat":"openai_chat"`) || strings.Contains(row.body, `"authStrategy":"dual_dummy"`) || strings.Contains(row.body, `"modelId":null`) {
			if lookups.Load() != before {
				t.Fatal("read saved credential before rejecting override")
			}
		}
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
	input, _ := json.Marshal(map[string]any{"now": now.Format(time.RFC3339Nano), "cases": cases})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Source provider connection HTTP %v %s", err, stderr.String())
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
			t.Fatalf("connection HTTP case %d\nGo %s\nSource %s", i, g, w)
		}
	}
	if requests.Load() != 12 {
		t.Fatalf("direct probes weren't executed: %d", requests.Load())
	}
}
