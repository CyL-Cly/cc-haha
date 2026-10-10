package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestDingTalkUnbindAndNonRegistrationRouteFallbackMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	seed := json.RawMessage(`{"future":{"keep":true},"telegram":{"botToken":"fake-other-bot","futureTelegram":7},"dingtalk":{"clientId":"fake-client","clientSecret":"fake-private-secret","permissionCardTemplateId":"old-card","allowedUsers":["U1"],"pairedUsers":[{"userId":"U2","displayName":"old","pairedAt":1}],"defaultWorkDir":"/private/old","endpoint":"https://private.invalid","futureDingTalk":{"keep":17}}}`)
	rows := []map[string]any{}
	for _, action := range []struct {
		method string
		tail   []string
		body   any
	}{{"POST", []string{"unbind"}, nil}, {"POST", []string{"unbind", "extra"}, nil}, {"GET", []string{"unbind"}, nil}, {"GET", []string{"registration", "begin"}, nil}, {"POST", []string{"unknown"}, nil}, {"PATCH", []string{"unbind"}, nil}, {"PUT", []string{"unbind"}, map[string]any{"dingtalk": map[string]any{"defaultWorkDir": "/private/new"}}}} {
		row := map[string]any{"seed": seed, "method": action.method, "tail": action.tail}
		if action.body != nil {
			row["body"] = action.body
		}
		rows = append(rows, row)
	}
	for _, value := range []json.RawMessage{nil, []byte(`null`), []byte(`false`), []byte(`17`), []byte(`"old"`), []byte(`["old"]`), []byte(`{}`)} {
		current := map[string]any{"future": map[string]any{"keep": true}, "slack": map[string]any{"appToken": "fake-slack-retained"}}
		if value != nil {
			current["dingtalk"] = value
		}
		rows = append(rows, map[string]any{"seed": current, "method": "POST", "tail": []string{"unbind"}})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-dingtalk-unbind-routes.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_DINGTALK_UNBIND_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source DingTalk routes", err, stderr.String())
	}
	var expected []any
	if json.Unmarshal(out, &expected) != nil || len(expected) != len(rows) {
		t.Fatal("actual Source response shape", string(out))
	}
	private := t.TempDir()
	service, err := adapterconfig.NewService(adapterconfig.ServiceOptions{ConfigDir: private, Store: storage.New(storage.Options{}), LogError: func(error) {}})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{options: Options{Adapters: service}}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := server.adaptersHTTP(w, r, strings.Split(strings.Trim(r.URL.Path, "/"), "/")); err != nil {
			writeError(w, err)
		}
	}))
	defer peer.Close()
	for i, row := range rows {
		initial, _ := json.Marshal(row["seed"])
		file := filepath.Join(private, "adapters.json")
		if err = os.WriteFile(file, initial, 0600); err != nil {
			t.Fatal(err)
		}
		var body io.Reader
		if value, ok := row["body"]; ok {
			raw, _ := json.Marshal(value)
			body = bytes.NewReader(raw)
		}
		req, _ := http.NewRequestWithContext(ctx, row["method"].(string), peer.URL+"/api/adapters/dingtalk/"+strings.Join(row["tail"].([]string), "/"), body)
		res, err := peer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var response, stored any
		if err = json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		persisted, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(persisted, &stored)
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]any{"status": float64(res.StatusCode), "body": response, "raw": stored, "mode": float64(info.Mode().Perm())}
		if !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("row%d actual HTTP/persistence %v Source %v", i, got, expected[i])
		}
	}
}
