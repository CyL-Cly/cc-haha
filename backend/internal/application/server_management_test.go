package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopManagementFactoryBindsRealServicesAndPublicCallback(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	service, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: work, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	host := httptest.NewServer(service)
	t.Cleanup(host.Close)
	request := func(method, path, body string, status int) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, host.URL+path, bytes.NewBufferString(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != status {
			t.Fatalf("%s %s: %d %s %v", method, path, res.StatusCode, data, err)
		}
		var result map[string]any
		if json.Unmarshal(data, &result) != nil {
			t.Fatal(string(data))
		}
		return result
	}
	request("GET", "/api/models", "", 200)
	request("GET", "/api/models/current", "", 200)
	request("GET", "/api/providers/auth-status", "", 200)
	if result := request("GET", "/api/haha-oauth", "", 200); result["loggedIn"] != false {
		t.Fatal(result)
	}
	request("GET", "/api/providers/claude-official/models", "", 200)
	request("GET", "/api/diagnostics/status", "", 200)
	for _, rangeName := range []string{"all", "7d", "30d"} {
		result := request("GET", "/api/activity-stats/"+rangeName, "", 200)
		if result["stats"] == nil || result["range"] != rangeName {
			t.Fatal(result)
		}
	}
	request("POST", "/api/diagnostics/events", `{"type":"factory-proof","summary":"Actual bound diagnostic persistence"}`, 200)
	events := request("GET", "/api/diagnostics/events", "", 200)["events"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["type"] != "factory-proof" {
		t.Fatal(events)
	}
	report := request("GET", "/api/doctor/report", "", 200)
	if report["report"] == nil {
		t.Fatal(report)
	}
	request("POST", "/api/doctor/repair", `{}`, 200)
	// Constructor wiring must use real catalog transport, not a ready stub.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer fake-catalog" {
			t.Error("unexpected upstream request", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"actual-loopback-model"}]}`)
	}))
	defer upstream.Close()
	input, _ := json.Marshal(map[string]string{"baseUrl": upstream.URL, "apiKey": "fake-catalog"})
	catalog := request("POST", "/api/providers/models", string(input), 200)
	if catalog["ok"] != true || catalog["models"].([]any)[0].(map[string]any)["id"] != "actual-loopback-model" {
		t.Fatal(catalog)
	}
	// Both unsaved and saved probes must reach the real upstream twice: direct
	// connectivity and the actual protocol proxy pipeline. The saved route reads
	// the newly persisted provider rather than a constructor-time snapshot.
	var probes atomic.Int32
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		var body protocol.JSONObject
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/v1/messages" || r.Header.Get("Authorization") != "Bearer fake-probe" || body.String("model") != "flash" {
			t.Error("unexpected probe request", r.URL.Path, body.String("model"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"type":"message","model":"flash","content":[{"type":"text","text":"ok"}]}`)
	}))
	defer probe.Close()
	checkProbe := func(result map[string]any) {
		t.Helper()
		steps := result["result"].(map[string]any)
		if steps["connectivity"].(map[string]any)["success"] != true || steps["proxy"].(map[string]any)["success"] != true {
			t.Fatal(steps)
		}
	}
	probeInput, _ := json.Marshal(map[string]any{"baseUrl": probe.URL, "apiKey": "fake-probe", "modelId": "flash[1m]", "apiFormat": "anthropic", "authStrategy": "auth_token", "supportsNestedToolResultMedia": false})
	checkProbe(request("POST", "/api/providers/test", string(probeInput), 200))
	providerInput, _ := json.Marshal(map[string]any{"presetId": "custom", "name": "Actual saved probe", "baseUrl": probe.URL, "apiKey": "fake-probe", "apiFormat": "anthropic", "authStrategy": "auth_token", "supportsNestedToolResultMedia": false, "models": map[string]string{"main": "flash[1m]", "haiku": "flash", "sonnet": "flash", "opus": "flash"}})
	providerID := request("POST", "/api/providers", string(providerInput), 201)["provider"].(map[string]any)["id"].(string)
	checkProbe(request("POST", "/api/providers/"+providerID+"/test", `{}`, 200))
	request("POST", "/api/providers/"+providerID+"/test", `{"baseUrl":"http://must-not-redirect.invalid"}`, 400)
	if probes.Load() != 4 {
		t.Fatal("wrong direct/proxy transport count", probes.Load())
	}
	res, err := host.Client().Get(host.URL + "/callback?error=fixture-rejection")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(data), "Login Failed") || strings.Contains(string(data), "<div id=\"root\"") {
		t.Fatal("callback was routed to SPA", res.StatusCode, string(data))
	}
	// Config operations are independent from OS permissions and helper boot.
	request("GET", "/api/computer-use/authorized-apps", "", 200)
	request("PUT", "/api/computer-use/authorized-apps", `{"enabled":false}`, 200)
	if err = service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
