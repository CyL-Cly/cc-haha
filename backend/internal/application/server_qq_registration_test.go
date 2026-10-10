package application

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDesktopQQUnknownSessionUsesActualRegistrationFactory(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	peer := httptest.NewServer(s)
	defer peer.Close()
	response, err := peer.Client().Post(peer.URL+"/api/adapters/qq/login/poll", "application/json", strings.NewReader(`{"sessionKey":"unknown-private"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), `"status":"not_started"`) {
		t.Fatalf("actual QQ factory must return Source not_started: %d %s %v", response.StatusCode, body, err)
	}
}

func TestQQRegistrationHTTPTransportUsesFrozenProxyAndVerifiedCA(t *testing.T) {
	env := map[string]string{"HTTPS_PROXY": "http://127.0.0.1:9876", "NO_PROXY": "private.test"}
	client, e := desktopQQHTTPClient(env)
	if e != nil {
		t.Fatal(e)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("connector requires native transport")
	}
	defer transport.CloseIdleConnections()
	env["HTTPS_PROXY"] = "http://unowned.invalid:1"
	env["NO_PROXY"] = "*"
	req, _ := http.NewRequest("POST", "https://q.qq.com/lite/create_bind_task", nil)
	proxy, e := transport.Proxy(req)
	if e != nil || proxy == nil || proxy.String() != "http://127.0.0.1:9876" {
		t.Fatal("ambient/mutated env replaced actual startup policy", proxy, e)
	}
	req, _ = http.NewRequest("GET", "https://private.test/x", nil)
	proxy, e = transport.Proxy(req)
	if e != nil || proxy != nil {
		t.Fatal("actual frozen no_proxy", proxy, e)
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.RootCAs == nil {
		t.Fatal("verified original CA missing")
	}
}

func TestQQActualOriginalHandlerValidationAndUnbindSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	seed := json.RawMessage(`{"future":17,"qq":{"appId":"old-id","appSecret":"old-secret","allowedUsers":["old"],"pairedUsers":[{"id":"old"}],"workspace":"keep"}}`)
	rows := []map[string]any{}
	for _, raw := range []string{"not JSON", "null", "[]", `{}`, `{"sessionKey":17}`, `{"sessionKey":""}`, `{"sessionKey":"   "}`, `{"sessionKey":"\ud800"}`, `{"sessionKey":"` + strings.Repeat("x", 257) + `"}`, `{"sessionKey":"` + strings.Repeat("😀", 128) + `"}`, `{"sessionKey":"` + strings.Repeat("😀", 129) + `"}`} {
		rows = append(rows, map[string]any{"seed": seed, "tail": []string{"login", "cancel"}, "raw": raw})
	}
	rows = append(rows, map[string]any{"seed": seed, "tail": []string{"unbind"}}, map[string]any{"seed": seed, "tail": []string{"unknown"}}, map[string]any{"seed": seed, "tail": []string{"login", "start"}, "method": "GET"})
	private := t.TempDir()
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-qq-registration-api.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = private
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "TMPDIR=" + private, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(private), "CC_HAHA_QQ_API_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var expected []map[string]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		o := nativeSessionOptions(t, "http://127.0.0.1:1")
		home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
		if e := os.MkdirAll(config, 0700); e != nil {
			t.Fatal(e)
		}
		file := filepath.Join(config, "adapters.json")
		if e := os.WriteFile(file, seed, 0600); e != nil {
			t.Fatal(e)
		}
		host, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
			return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		}})
		if e != nil {
			t.Fatal(e)
		}
		peer := httptest.NewServer(host)
		tail := row["tail"].([]string)
		method, _ := row["method"].(string)
		if method == "" {
			method = "POST"
		}
		raw, _ := row["raw"].(string)
		if raw == "" {
			raw = "{}"
		}
		req, _ := http.NewRequest(method, peer.URL+"/api/adapters/qq/"+strings.Join(tail, "/"), strings.NewReader(raw))
		res, e := peer.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		p, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		var body any
		_ = json.Unmarshal(p, &body)
		stored, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		normalized, e := protocol.ParseAndStringifyJavaScriptJSON(stored)
		if e != nil {
			t.Fatal(e)
		}
		var rawStored any
		_ = json.Unmarshal(stored, &rawStored)
		info, e := os.Stat(file)
		if e != nil {
			t.Fatal(e)
		}
		calls := []any{}
		if res.StatusCode == 200 && tail[0] == "login" && tail[1] == "cancel" {
			var value protocol.JSONObject
			_ = value.UnmarshalJSON([]byte(raw))
			var key any
			_ = json.Unmarshal(value.Get("sessionKey"), &key)
			calls = append(calls, map[string]any{"action": "cancel", "key": key})
		}
		got := map[string]any{"status": float64(res.StatusCode), "body": body, "calls": calls, "raw": rawStored, "storedJson": string(normalized), "mode": float64(info.Mode().Perm())}
		peer.Close()
		if e := host.Close(context.Background()); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(got, expected[i]) {
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(expected[i])
			t.Fatalf("actualfactory Sourcehandler row%d\nGo%s\nSource%s", i, a, b)
		}
	}

	t.Logf("%d actual original handler validation/unbind profiles matched actual NewDesktopServer HTTP/private storage", len(rows))
}

func TestQQRegistrationConstructorUsesActualTLSConnectorAndJoin(t *testing.T) {
	var mu sync.Mutex
	var bindKey []byte
	calls := []string{}
	workerPoll := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "q.qq.com" || r.Method != "POST" {
			t.Error("actual vendor target", r.Host, r.Method)
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		mu.Lock()
		calls = append(calls, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/lite/create_bind_task" {
			var e error
			bindKey, e = base64.StdEncoding.DecodeString(body["key"].(string))
			if e != nil {
				t.Error(e)
			}
			io.WriteString(w, `{"retcode":0,"data":{"task_id":"actual-private-task"}}`)
			return
		}
		if body["task_id"] != "actual-private-task" {
			t.Error("actual prior create result", body)
		}
		workerPoll <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		block, e := aes.NewCipher(bindKey)
		if e != nil {
			t.Error(e)
			return
		}
		gcm, e := cipher.NewGCM(block)
		if e != nil {
			t.Error(e)
			return
		}
		nonce := bytes.Repeat([]byte{7}, 12)
		payload := append(append([]byte(nil), nonce...), gcm.Seal(nil, nonce, []byte("actual-private-secret"), nil)...)
		json.NewEncoder(w).Encode(map[string]any{"retcode": 0, "data": map[string]any{"status": 2, "bot_appid": "actual-private-id", "bot_encrypt_secret": base64.StdEncoding.EncodeToString(payload), "user_openid": "private-user"}})
	}))
	defer upstream.Close()
	defer once.Do(func() { close(release) })
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	address := strings.TrimPrefix(upstream.URL, "https://")
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com"}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != "q.qq.com:443" {
			return nil, fmt.Errorf("unowned actual socket target %s", target)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	service, e := newDesktopQQRegistration(&http.Client{Transport: transport}, map[string]string{})
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close(context.Background())
	start, e := service.Start(context.Background(), protocol.NewJSONObject())
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(start.String("verificationUrl"), "task_id=actual-private-task") || !strings.Contains(start.String("verificationUrl"), "source=claude-code-haha") {
		t.Fatal("actual connector URL", start)
	}
	select {
	case <-workerPoll:
	case <-time.After(3 * time.Second):
		t.Fatal("actual physical vendor poll absent")
	}
	once.Do(func() { close(release) })
	deadline := time.After(3 * time.Second)
	for {
		v, e := service.PollJSON(context.Background(), start.Get("sessionKey"))
		if e != nil {
			t.Fatal(e)
		}
		if string(v.Get("connected")) == "true" {
			if v.String("appId") != "actual-private-id" || v.String("appSecret") != "actual-private-secret" {
				t.Fatal("actual AESGCM credentials", v)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("actual TLS connector success not latched")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if e := service.Close(context.Background()); e != nil {
		t.Fatal("owned connector close", e)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"/lite/create_bind_task", "/lite/poll_bind_result"}) {
		t.Fatal("actual create/poll count", calls)
	}
}

func TestDesktopQQRegistrationUsesActualTLSConnectorStorageAndUnbind(t *testing.T) {
	var mu sync.Mutex
	var bindKey []byte
	calls := []string{}
	workerPoll := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "q.qq.com" || r.Method != "POST" {
			t.Error("actual vendor target", r.Host, r.Method)
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		mu.Lock()
		calls = append(calls, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/lite/create_bind_task" {
			var e error
			bindKey, e = base64.StdEncoding.DecodeString(body["key"].(string))
			if e != nil {
				t.Error(e)
			}
			io.WriteString(w, `{"retcode":0,"data":{"task_id":"actual-private-task"}}`)
			return
		}
		if body["task_id"] != "actual-private-task" {
			t.Error("actual prior create result", body)
		}
		workerPoll <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		block, e := aes.NewCipher(bindKey)
		if e != nil {
			t.Error(e)
			return
		}
		gcm, e := cipher.NewGCM(block)
		if e != nil {
			t.Error(e)
			return
		}
		nonce := bytes.Repeat([]byte{7}, 12)
		payload := append(append([]byte(nil), nonce...), gcm.Seal(nil, nonce, []byte("actual-private-secret"), nil)...)
		json.NewEncoder(w).Encode(map[string]any{"retcode": 0, "data": map[string]any{"status": 2, "bot_appid": "actual-private-id", "bot_encrypt_secret": base64.StdEncoding.EncodeToString(payload), "user_openid": "private-user"}})
	}))
	defer upstream.Close()
	defer once.Do(func() { close(release) })
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	address := strings.TrimPrefix(upstream.URL, "https://")
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com"}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != "q.qq.com:443" {
			return nil, fmt.Errorf("unowned actual socket target %s", target)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(config, "adapters.json")
	seed := []byte(`{"future":17,"qq":{"appId":"old-id","appSecret":"old-secret","allowedUsers":["old-user"],"pairedUsers":[{"id":"old-user"}],"workspace":"keep"}}`)
	if e := os.WriteFile(file, seed, 0600); e != nil {
		t.Fatal(e)
	}
	host, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, QQRegistrationHTTPClient: &http.Client{Transport: transport}, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer host.Close(context.Background())
	peer := httptest.NewServer(host)
	defer peer.Close()
	request := func(tail string, p []byte) protocol.JSONObject {
		t.Helper()
		res, e := peer.Client().Post(peer.URL+"/api/adapters/qq/"+tail, "application/json", bytes.NewReader(p))
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil || res.StatusCode != 200 {
			t.Fatalf("actual QQ HTTP %s %d %s %v", tail, res.StatusCode, raw, e)
		}
		var result protocol.JSONObject
		if e = result.UnmarshalJSON(raw); e != nil {
			t.Fatal(e)
		}
		return result
	}
	start := request("login/start", []byte("ignored invalidJSON"))
	if !strings.HasPrefix(start.String("qrDataUrl"), "data:image/png;base64,") {
		t.Fatal("actual start QR")
	}

	if !strings.Contains(start.String("verificationUrl"), "task_id=actual-private-task") || !strings.Contains(start.String("verificationUrl"), "source=claude-code-haha") {
		t.Fatal("actual connector URL", start)
	}
	select {
	case <-workerPoll:
	case <-time.After(3 * time.Second):
		t.Fatal("actual physical vendor poll absent")
	}
	once.Do(func() { close(release) })
	deadline := time.After(3 * time.Second)
	input, _ := json.Marshal(map[string]json.RawMessage{"sessionKey": start.Get("sessionKey")})
	for {
		v := request("login/poll", input)
		var section protocol.JSONObject
		_ = section.UnmarshalJSON(v.Get("qq"))
		if section.IsObject() {
			if section.String("appId") != "actual-private-id" || section.String("appSecret") != "****cret" || string(section.Get("allowedUsers")) != "[]" || string(section.Get("pairedUsers")) != "[]" || section.String("workspace") != "keep" {
				t.Fatal("actual success mask/reset/unknown", v)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("actual TLS success not stored")
		case <-time.After(5 * time.Millisecond):
		}
	}
	qqAssertOriginalSuccessConsumer(t, seed, start.Get("sessionKey"), file)
	persisted, e := os.ReadFile(file)
	if e != nil || !strings.Contains(string(persisted), `"actual-private-secret"`) {
		t.Fatal("actual private raw secret", e)
	}
	absent := request("login/poll", input)
	if absent.String("status") != "not_started" {
		t.Fatal("terminal not consumed", absent)
	}
	unbound := request("unbind", nil)
	var section protocol.JSONObject
	_ = section.UnmarshalJSON(unbound.Get("qq"))
	if section.Get("appId") != nil || section.Get("appSecret") != nil || section.String("workspace") != "keep" {
		t.Fatal("actual unbind", unbound)
	}
	if e := host.Close(context.Background()); e != nil {
		t.Fatal("actual ServerClose connector drain", e)
	}

	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"/lite/create_bind_task", "/lite/poll_bind_result"}) {
		t.Fatal("actual create/poll count", calls)
	}
}

func qqAssertOriginalSuccessConsumer(t *testing.T, seed []byte, key json.RawMessage, file string) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	raw, _ := json.Marshal(map[string]json.RawMessage{"sessionKey": key})
	input, _ := json.Marshal(map[string]any{"rows": []any{map[string]any{"seed": json.RawMessage(seed), "tail": []string{"login", "poll"}, "raw": string(raw), "result": map[string]any{"connected": true, "appId": "actual-private-id", "appSecret": "actual-private-secret", "userOpenid": "private-user"}}}})
	private := t.TempDir()
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-qq-registration-api.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = private
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "TMPDIR=" + private, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(private), "CC_HAHA_QQ_API_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var expected []map[string]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	persisted, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	normalized, e := protocol.ParseAndStringifyJavaScriptJSON(persisted)
	if e != nil {
		t.Fatal(e)
	}
	var actual any
	_ = json.Unmarshal(persisted, &actual)
	if !reflect.DeepEqual(actual, expected[0]["raw"]) || string(normalized) != expected[0]["storedJson"] {
		t.Fatal("actualTLS credentials originalhandler storage byteprojection mismatch", string(normalized), expected[0])
	}
}

func TestDesktopQQServerCloseOwnsActiveVendorHTTPAndRejectsLateSave(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lite/create_bind_task" {
			io.WriteString(w, `{"retcode":0,"data":{"task_id":"private-active-close"}}`)
			return
		}
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
		io.WriteString(w, `{"retcode":0,"data":{"status":0}}`)
	}))
	defer upstream.Close()
	defer once.Do(func() { close(release) })
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	address := strings.TrimPrefix(upstream.URL, "https://")
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com"}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != "q.qq.com:443" {
			return nil, fmt.Errorf("unowned QQtarget")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(config, "adapters.json")
	seed := []byte(`{"qq":{"appSecret":"old-secret"},"future":17}`)
	if e := os.WriteFile(file, seed, 0600); e != nil {
		t.Fatal(e)
	}
	host, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, QQRegistrationHTTPClient: &http.Client{Transport: transport}, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer host.Close(context.Background())
	peer := httptest.NewServer(host)
	defer peer.Close()
	res, e := peer.Client().Post(peer.URL+"/api/adapters/qq/login/start", "application/json", nil)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(res.Body)
	res.Body.Close()
	if e != nil || res.StatusCode != 200 {
		t.Fatal("actual QQstart", string(raw), e)
	}
	var start protocol.JSONObject
	_ = start.UnmarshalJSON(raw)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("actual activevendorHTTP absent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = host.Close(ctx); e != nil {
		t.Fatal("actual ServerClose vendorjoin", e)
	}
	once.Do(func() { close(release) })
	after, e := os.ReadFile(file)
	if e != nil || string(after) != string(seed) {
		t.Fatal("close changed private binding", e)
	}
	p, _ := json.Marshal(map[string]json.RawMessage{"sessionKey": start.Get("sessionKey")})
	res, e = peer.Client().Post(peer.URL+"/api/adapters/qq/login/poll", "application/json", bytes.NewReader(p))
	if e == nil {
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode == 200 {
			t.Fatal("closed service accepted poll")
		}
	}
}
