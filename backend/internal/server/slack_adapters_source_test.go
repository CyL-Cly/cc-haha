package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSlackOriginalManifestUnbindLegacyUnknownFieldsAndPhysicalREST(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	seed := json.RawMessage(`{"future":{"keep":true},"telegram":{"botToken":"fake-other-bot","futureTelegram":7},"slack":{"botToken":"private-slack-bot","appToken":"private-slack-app","allowedUsers":["U1"],"pairedUsers":[{"userId":"U2","displayName":"old","pairedAt":1}],"defaultWorkDir":"/private/old","futureSlack":"preserve"}}`)
	actions := []map[string]any{{"method": "GET", "tail": []string{"manifest"}}, {"method": "GET", "tail": []string{"manifest", "extra"}}, {"method": "POST", "tail": []string{"unbind"}}, {"method": "GET", "tail": []string{"unbind"}}, {"method": "POST", "tail": []string{"unknown"}}, {"method": "GET", "tail": []string{}}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-slack-config-routes.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SLACK_CONFIG_ROUTES_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Dir = home
	input, _ := json.Marshal(map[string]any{"seed": seed, "actions": actions})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var want any
	if json.Unmarshal(out, &want) != nil {
		t.Fatal(string(out))
	}
	service, err := adapterconfig.NewService(adapterconfig.ServiceOptions{ConfigDir: config, Store: storage.New(storage.Options{}), LogError: func(error) {}})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{options: Options{Adapters: service}}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := []string{"api", "adapters", "slack"}
		tail := r.URL.Query()["tail"]
		parts = append(parts, tail...)
		if err := server.adaptersHTTP(w, r, parts); err != nil {
			if e, ok := err.(*apiError); ok {
				w.WriteHeader(e.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"status": e.status, "code": e.code, "message": e.message})
			} else {
				t.Error(err)
			}
		}
	}))
	defer peer.Close()
	rows := []any{}
	for _, a := range actions {
		_ = os.WriteFile(filepath.Join(config, "adapters.json"), seed, 0600)
		address := peer.URL + "/?"
		for _, tail := range a["tail"].([]string) {
			address += "tail=" + tail + "&"
		}
		req, _ := http.NewRequest(a["method"].(string), address, nil)
		res, err := peer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(res.Body)
		res.Body.Close()
		var body any
		_ = json.Unmarshal(data, &body)
		if res.StatusCode == 200 {
			stored, _ := os.ReadFile(filepath.Join(config, "adapters.json"))
			var current any
			_ = json.Unmarshal(stored, &current)
			info, _ := os.Stat(filepath.Join(config, "adapters.json"))
			rows = append(rows, map[string]any{"status": res.StatusCode, "body": body, "raw": current, "mode": int(info.Mode().Perm())})
		} else {
			rows = append(rows, body)
		}
	}
	got, _ := json.Marshal(rows)
	var actual any
	_ = json.Unmarshal(got, &actual)
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("actual original Slack routes got%s want%s", got, out)
	}
}

func TestSlackOriginalUnbindWriteFailureRetainsActualFileError(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-slack-route-failure.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SLACK_ROUTE_FAILURE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Dir = home
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var want any
	if json.Unmarshal(out, &want) != nil {
		t.Fatal(string(out))
	}
	service, err := adapterconfig.NewService(adapterconfig.ServiceOptions{ConfigDir: config, Store: storage.New(storage.Options{}), LogError: func(error) {}})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{options: Options{Adapters: service}}
	req := httptest.NewRequest("POST", "/api/adapters/slack/unbind", nil)
	err = server.adaptersHTTP(httptest.NewRecorder(), req, []string{"api", "adapters", "slack", "unbind"})
	e, ok := err.(*apiError)
	if !ok {
		t.Fatal("missing actual typed file failure", err)
	}
	got := map[string]any{"status": float64(e.status), "code": e.code, "message": e.message}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actual Source file failure got%v want%s", got, out)
	}
}
