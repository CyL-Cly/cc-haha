package application

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

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopDingTalkRegistrationUsesActualHTTPAndSharedPrivateConfig(t *testing.T) {
	var upstreamPaths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPaths = append(upstreamPaths, r.URL.Path)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/app/registration/init":
			if body["source"] != "PRIVATE_SOURCE" {
				t.Error("actual configured registration source", body)
			}
			io.WriteString(w, `{"errcode":0,"nonce":" nonce-private "}`)
		case "/app/registration/begin":
			if body["nonce"] != "nonce-private" {
				t.Error("actual prior nonce", body)
			}
			io.WriteString(w, `{"errcode":0,"device_code":"device-private","verification_uri_complete":"https://private.test/confirm?code=fake","expires_in":17,"interval":2}`)
		case "/app/registration/poll":
			if body["device_code"] == "failure" {
				io.WriteString(w, `{"errcode":0,"status":"SUCCESS"}`)
			} else {
				if body["device_code"] != "device-private" {
					t.Error("actual saved device code", body)
				}
				io.WriteString(w, `{"errcode":0,"status":"SUCCESS","client_id":"fake-client","client_secret":"fake-secret"}`)
			}
		default:
			t.Error("unexpected registration URL", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	o.Environment["DINGTALK_REGISTRATION_BASE_URL"] = upstream.URL
	o.Environment["DINGTALK_REGISTRATION_SOURCE"] = " PRIVATE_SOURCE "
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(config, "adapters.json")
	if err := os.WriteFile(file, []byte(`{"future":{"keep":17},"telegram":{"botToken":"fake-other"},"dingtalk":{"allowedUsers":["private-user"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: home, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	peer := httptest.NewServer(service)
	defer peer.Close()
	request := func(path string, value any, expected int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(value)
		res, e := peer.Client().Post(peer.URL+path, "application/json", bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		if e != nil || res.StatusCode != expected {
			t.Fatalf("actual desktop registration %s: %d %s %v", path, res.StatusCode, data, e)
		}
		var body map[string]any
		if e = json.Unmarshal(data, &body); e != nil {
			t.Fatal(e)
		}
		return body
	}
	begin := request("/api/adapters/dingtalk/registration/begin", map[string]any{}, 200)
	if begin["deviceCode"] != "device-private" || begin["expiresInSeconds"] != float64(17) || begin["intervalSeconds"] != float64(2) || begin["qrDataUrl"] == nil {
		t.Fatal("actual begin payload", begin)
	}
	poll := request("/api/adapters/dingtalk/registration/poll", map[string]any{"deviceCode": begin["deviceCode"]}, 200)
	if poll["status"] != "SUCCESS" {
		t.Fatal(poll)
	}
	masked := poll["config"].(map[string]any)
	if masked["dingtalk"].(map[string]any)["clientSecret"] != "****cret" {
		t.Fatal("saved credentials not masked", masked)
	}
	data, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	var actual map[string]any
	_ = json.Unmarshal(data, &actual)
	if actual["dingtalk"].(map[string]any)["clientSecret"] != "fake-secret" || actual["telegram"].(map[string]any)["botToken"] != "fake-other" || actual["future"].(map[string]any)["keep"] != float64(17) {
		t.Fatal("actual private persistence", actual)
	}
	if len(upstreamPaths) != 3 {
		t.Fatal("physical request count", upstreamPaths)
	}
	failed := request("/api/adapters/dingtalk/registration/poll", map[string]any{"deviceCode": "failure"}, 500)
	if len(failed) != 1 || failed["error"] != "Internal server error" {
		t.Fatal("original outer rejection boundary", failed)
	}
	bad := request("/api/adapters/dingtalk/registration/poll", map[string]any{"deviceCode": 17}, 400)
	if bad["error"] != "BAD_REQUEST" {
		t.Fatal("original inline validation boundary", bad)
	}
}

func TestDesktopDingTalkPollRouteMatchesActualSourceUTF16AndValidation(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	seed := map[string]any{"future": 17, "dingtalk": map[string]any{"allowedUsers": []string{"retained"}}}
	inputs := []string{"not JSON", "null", "[]", "{}", `{"deviceCode":17}`, `{"deviceCode":""}`, `{"deviceCode":" \ufeff "}`, `{"deviceCode":"\ufeffprivate\ufeff"}`, `{"deviceCode":" \ud800 "}`}
	for _, text := range []string{strings.Repeat("x", 257), strings.Repeat("😀", 128), strings.Repeat("😀", 129)} {
		raw, _ := json.Marshal(map[string]string{"deviceCode": text})
		inputs = append(inputs, string(raw))
	}
	var rows []any
	for _, raw := range inputs {
		rows = append(rows, map[string]any{"seed": seed, "action": "poll", "rawInput": raw, "recordRaw": true, "replies": []any{map[string]any{"body": map[string]any{"errcode": 0, "status": "PENDING"}}}})
	}
	private := t.TempDir()
	if err := os.MkdirAll(filepath.Join(private, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-dingtalk-registration.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	input, _ := json.Marshal(map[string]any{"rows": rows})
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = private
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "TMPDIR=" + private, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(private), "CC_HAHA_DINGTALK_REGISTRATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source", err, stderr.String())
	}
	var expected []map[string]any
	if err = json.Unmarshal(output, &expected); err != nil || len(expected) != len(inputs) {
		t.Fatal("actual Source rows", err)
	}
	var requests []any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
		}
		var body any
		if e = json.Unmarshal(raw, &body); e != nil {
			t.Error(e)
		}
		requests = append(requests, map[string]any{"path": r.URL.Path, "method": r.Method, "body": body, "rawBody": string(raw)})
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"errcode":0,"status":"PENDING"}`)
	}))
	defer upstream.Close()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	o.Environment["DINGTALK_REGISTRATION_BASE_URL"] = upstream.URL
	if err = os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(config, "adapters.json")
	service, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: home, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	peer := httptest.NewServer(service)
	defer peer.Close()
	for i, raw := range inputs {
		requests = []any{}
		initial, _ := json.Marshal(seed)
		if err = os.WriteFile(file, initial, 0600); err != nil {
			t.Fatal(err)
		}
		res, e := peer.Client().Post(peer.URL+"/api/adapters/dingtalk/registration/poll", "application/json", strings.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		bodyRaw, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		var body any
		if e = json.Unmarshal(bodyRaw, &body); e != nil {
			t.Fatal(e)
		}
		storedRaw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		var stored any
		if e = json.Unmarshal(storedRaw, &stored); e != nil {
			t.Fatal(e)
		}
		delete(expected[i], "qr")
		got := map[string]any{"status": float64(res.StatusCode), "body": body, "requests": requests, "raw": stored}
		if !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("row%d actual desktop route: Go %v Source %v", i, got, expected[i])
		}
	}
}
