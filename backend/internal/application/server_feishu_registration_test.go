package application

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopFeishuUnknownSessionUsesActualRegistrationFactory(t *testing.T) {
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
	response, err := peer.Client().Post(peer.URL+"/api/adapters/feishu/registration/poll", "application/json", strings.NewReader(`{"sessionKey":"unknown-private"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), `"status":"not_started"`) {
		t.Fatalf("actual Feishu factory must return Source not_started: %d %s %v", response.StatusCode, body, err)
	}
}

type feishuFactoryRedirect struct {
	destination *url.URL
	transport   http.RoundTripper
}

func (f feishuFactoryRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || (req.URL.Host != "accounts.feishu.cn" && req.URL.Host != "accounts.larksuite.com") || req.URL.Path != "/oauth/v1/app/registration" {
		return nil, fmt.Errorf("unowned Feishu target")
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = f.destination.Scheme
	u.Host = f.destination.Host
	clone.URL = &u
	clone.Header.Set("X-Logical-Host", req.URL.Host)
	return f.transport.RoundTrip(clone)
}
func TestDesktopFeishuPhysicalBeginLarkSuccessCancelUnbindAndDisconnect(t *testing.T) {
	var mu sync.Mutex
	calls := []string{}
	pollStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	polls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
		}
		params, e := url.ParseQuery(string(body))
		if e != nil {
			t.Error(e)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("wire content type", r.Header)
		}
		mu.Lock()
		calls = append(calls, r.Header.Get("X-Logical-Host")+":"+params.Get("action"))
		mu.Unlock()
		switch params.Get("action") {
		case "begin":
			if string(body) != "action=begin&archetype=PersonalAgent&auth_method=client_secret&request_user_info=open_id" {
				t.Error("begin body", string(body))
			}
			io.WriteString(w, `{"device_code":"private-device","verification_uri_complete":"https://open.feishu.cn/launcher?code=fake","expires_in":600,"interval":2}`)
		case "poll":
			if params.Get("device_code") != "private-device" {
				t.Error("private actual device", params)
			}
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			if n == 1 {
				io.WriteString(w, `{"error":"authorization_pending","user_info":{"tenant_brand":"lark"}}`)
				return
			}
			if n == 3 {
				pollStarted <- struct{}{}
				<-release
			}
			io.WriteString(w, `{"client_id":"private-id","client_secret":"private-secret","user_info":{"open_id":"ou-new"}}`)
		default:
			t.Error("unexpected action", params)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	destination, _ := url.Parse(upstream.URL)
	client := &http.Client{Transport: feishuFactoryRedirect{destination, upstream.Client().Transport}}
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(config, "adapters.json")
	seed := `{"future":{"keep":17},"telegram":{"botToken":"fake-other"},"feishu":{"appId":"old","appSecret":"old-secret","encryptKey":"old-encrypted","verificationToken":"old-verify","domain":"lark","allowedUsers":["old-open-id"],"pairedUsers":[{"id":"old"}],"workspace":"private-workspace","future":true}}`
	if e := os.WriteFile(file, []byte(seed), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, FeishuRegistrationHTTPClient: client, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	peer := httptest.NewServer(s)
	defer peer.Close()
	request := func(tail, raw string, status int) map[string]any {
		t.Helper()
		res, e := peer.Client().Post(peer.URL+"/api/adapters/feishu/"+tail, "application/json", strings.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		p, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil || res.StatusCode != status {
			t.Fatalf("actual %s %d %s %v", tail, res.StatusCode, p, e)
		}
		var v map[string]any
		if e = json.Unmarshal(p, &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	begin := request("registration/begin", "not JSON intentionally ignored", 200)
	key, ok := begin["sessionKey"].(string)
	if !ok {
		t.Fatal(begin)
	}
	if _, e := uuid.Parse(key); e != nil {
		t.Fatal("actual generated session UUID", e)
	}
	u, e := url.Parse(begin["verificationUri"].(string))
	if e != nil || u.Query().Get("name") != "Claude Code Haha" || u.Query().Get("desc") != "把飞书私聊接到本机的 Claude Code 会话。" || u.Query().Get("createOnly") != "true" {
		t.Fatal("actual app prefills", begin, e)
	}
	pngBytes, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(begin["qrDataUrl"].(string), "data:image/png;base64,"))
	if e != nil {
		t.Fatal(e)
	}
	img, e := png.Decode(bytes.NewReader(pngBytes))
	if e != nil || img.Bounds().Dx() != 220 {
		t.Fatal("actual QR PNG", e)
	}
	rawKey, _ := json.Marshal(map[string]string{"sessionKey": key})
	result := request("registration/poll", string(rawKey), 200)
	if result["status"] != "success" {
		t.Fatal(result)
	}
	masked := result["config"].(map[string]any)["feishu"].(map[string]any)
	if masked["appSecret"] != "****cret" || masked["domain"] != "lark" {
		t.Fatal("actual mask/domain", masked)
	}
	checkStored := func(removed bool) {
		t.Helper()
		p, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		var v map[string]any
		_ = json.Unmarshal(p, &v)
		f := v["feishu"].(map[string]any)
		if v["future"].(map[string]any)["keep"] != float64(17) || v["telegram"].(map[string]any)["botToken"] != "fake-other" || f["workspace"] != "private-workspace" || f["future"] != true || len(f["allowedUsers"].([]any)) != 0 || len(f["pairedUsers"].([]any)) != 0 {
			t.Fatal("unknown-preserving actual storage", v)
		}
		if removed {
			for _, k := range []string{"appId", "appSecret", "encryptKey", "verificationToken", "domain"} {
				if _, ok := f[k]; ok {
					t.Fatal("unbind retained credential", k, f)
				}
			}
		} else if f["appSecret"] != "private-secret" || f["encryptKey"] != "" || f["verificationToken"] != "" {
			t.Fatal("credential cleanup", f)
		}
		info, e := os.Stat(file)
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal("actual private file mode", e)
		}
	}
	checkStored(false)
	request("registration/poll", string(rawKey), 200)
	request("unbind", `{}`, 200)
	checkStored(true)
	begin = request("registration/begin", `{}`, 200)
	rawKey, _ = json.Marshal(map[string]any{"sessionKey": begin["sessionKey"]})
	request("registration/cancel", string(rawKey), 200)
	absent := request("registration/poll", string(rawKey), 200)
	if absent["status"] != "not_started" {
		t.Fatal(absent)
	}
	// Disconnecting the renderer only settles the response; the source business
	// promise continues, including success storage, until the server owns close.
	begin = request("registration/begin", `{}`, 200)
	rawKey, _ = json.Marshal(map[string]any{"sessionKey": begin["sessionKey"]})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", peer.URL+"/api/adapters/feishu/registration/poll", bytes.NewReader(rawKey))
	done := make(chan error, 1)
	go func() {
		res, e := peer.Client().Do(req)
		if res != nil {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}
		done <- e
	}()
	select {
	case <-pollStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("actual poll did not begin")
	}
	cancel()
	if e := <-done; e == nil {
		t.Fatal("renderer abort did not settle")
	}
	request("registration/cancel", string(rawKey), 200)
	close(release)
	deadline := time.After(3 * time.Second)
	for {
		p, e := os.ReadFile(file)
		if e == nil && strings.Contains(string(p), `"private-secret"`) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("in-flight success lost after cancel/disconnect")
		case <-time.After(5 * time.Millisecond):
		}
	}
	checkStored(false)
	for _, raw := range []string{"not JSON", "null", "[]", `{}`, `{"sessionKey":17}`, `{"sessionKey":""}`, `{"sessionKey":"` + strings.Repeat("x", 257) + `"}`} {
		request("registration/cancel", raw, 400)
	}
	request("registration/cancel", `{"sessionKey":"   "}`, 200)
	request("unknown", `{}`, 404)
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"accounts.feishu.cn:begin", "accounts.feishu.cn:poll", "accounts.larksuite.com:poll", "accounts.feishu.cn:begin", "accounts.feishu.cn:begin", "accounts.feishu.cn:poll"}) {
		t.Fatal("actual one-way Lark/wire call sequence", calls)
	}
}

func TestDesktopFeishuRoutesMatchActualSourceAndPhysicalPrivateStorage(t *testing.T) {
	seed := map[string]any{"future": 17, "telegram": map[string]any{"botToken": "other-fake"}, "feishu": map[string]any{"appId": "old-id", "appSecret": "old-secret", "encryptKey": "old-encrypted", "verificationToken": "old-verify", "domain": "lark", "allowedUsers": []string{"old-user"}, "pairedUsers": []any{map[string]any{"id": "old-user"}}, "workspace": "retained", "future": true}}
	begin := map[string]any{"device_code": " private-device ", "verification_uri_complete": "https://open.feishu.cn/page?x=1", "expires_in": 600, "interval": 2}
	type reply struct {
		Body   any    `json:"body,omitempty"`
		Raw    string `json:"raw,omitempty"`
		Status int    `json:"status,omitempty"`
	}
	type action struct {
		Kind   string   `json:"kind,omitempty"`
		Raw    string   `json:"raw,omitempty"`
		Tail   []string `json:"tail,omitempty"`
		Method string   `json:"method,omitempty"`
	}
	type row struct {
		Route   bool     `json:"route"`
		Seed    any      `json:"seed"`
		Actions []action `json:"actions"`
		Replies []reply  `json:"replies"`
	}
	rows := []row{
		{true, seed, []action{{Kind: "begin"}, {Kind: "poll"}, {Kind: "poll"}, {Tail: []string{"unbind"}}, {Tail: []string{"unbind", "extra"}}}, []reply{{Body: begin}, {Body: map[string]any{"user_info": map[string]any{"tenant_brand": "lark"}, "error": "authorization_pending"}}, {Body: map[string]any{"client_id": "new-id", "client_secret": "new-secret"}}}},
		{true, seed, []action{{Kind: "begin"}, {Kind: "cancel"}, {Kind: "poll"}}, []reply{{Body: begin}}},
		{true, seed, []action{{Kind: "begin"}, {Kind: "poll"}, {Kind: "poll"}}, []reply{{Body: begin}, {Status: 400, Body: map[string]any{"error": "slow_down"}}, {Body: map[string]any{"error": "expired_token"}}}},
		{true, seed, []action{{Kind: "begin"}, {Kind: "poll"}}, []reply{{Body: begin}, {Raw: "null"}}},
		{true, seed, []action{{Kind: "begin"}}, []reply{{Body: map[string]any{"device_code": "private", "verification_uri_complete": "https://evil.test/x"}}}},
		{true, seed, []action{{Tail: []string{"unknown"}}, {Tail: []string{"registration", "begin"}, Method: "GET"}}, nil},
	}
	for _, raw := range []string{"not JSON", "null", "[]", `{}`, `{"sessionKey":17}`, `{"sessionKey":""}`, `{"sessionKey":"   "}`, `{"sessionKey":"\ud800"}`, `{"sessionKey":"\ud801"}`, `{"sessionKey":"` + strings.Repeat("x", 257) + `"}`, `{"sessionKey":"` + strings.Repeat("😀", 128) + `"}`, `{"sessionKey":"` + strings.Repeat("😀", 129) + `"}`} {
		rows = append(rows, row{true, seed, []action{{Kind: "poll", Raw: raw}, {Kind: "cancel", Raw: raw}}, nil})
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	private := t.TempDir()
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-feishu-registration.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = private
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "TMPDIR=" + private, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(private), "CC_HAHA_FEISHU_REGISTRATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
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
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var mu sync.Mutex
			at := 0
			requests := []any{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
				}
				mu.Lock()
				requests = append(requests, map[string]any{"url": "https://" + r.Header.Get("X-Logical-Host") + "/oauth/v1/app/registration", "method": r.Method, "contentType": r.Header.Get("Content-Type"), "body": string(p)})
				if at >= len(row.Replies) {
					mu.Unlock()
					t.Error("unexpected platform HTTP")
					w.WriteHeader(500)
					return
				}
				v := row.Replies[at]
				at++
				mu.Unlock()
				status := v.Status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				if v.Raw != "" {
					io.WriteString(w, v.Raw)
				} else {
					json.NewEncoder(w).Encode(v.Body)
				}
			}))
			defer upstream.Close()
			destination, _ := url.Parse(upstream.URL)
			client := &http.Client{Transport: feishuFactoryRedirect{destination, upstream.Client().Transport}}
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
			if e := os.MkdirAll(config, 0700); e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(config, "adapters.json")
			p, _ := json.Marshal(row.Seed)
			if e := os.WriteFile(file, p, 0600); e != nil {
				t.Fatal(e)
			}
			s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, FeishuRegistrationHTTPClient: client, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
				return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			}})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			peer := httptest.NewServer(s)
			defer peer.Close()
			values := []any{}
			key := "private-fixed-key"
			for _, a := range row.Actions {
				tail := a.Tail
				if tail == nil {
					tail = []string{"registration", a.Kind}
				}
				raw := a.Raw
				if raw == "" {
					p, _ := json.Marshal(map[string]string{"sessionKey": key})
					raw = string(p)
				}
				method := a.Method
				if method == "" {
					method = "POST"
				}
				req, _ := http.NewRequest(method, peer.URL+"/api/adapters/feishu/"+strings.Join(tail, "/"), strings.NewReader(raw))
				res, e := peer.Client().Do(req)
				if e != nil {
					t.Fatal(e)
				}
				p, e := io.ReadAll(res.Body)
				res.Body.Close()
				if e != nil {
					t.Fatal(e)
				}
				var body map[string]any
				if e = json.Unmarshal(p, &body); e != nil {
					t.Fatal(e)
				}
				if v, ok := body["sessionKey"].(string); ok {
					key = v
					body["sessionKey"] = "<uuid>"
				}
				if text, ok := body["verificationUri"].(string); ok {
					u, e := url.Parse(text)
					if e != nil {
						t.Fatal(e)
					}
					q := u.Query()
					compressed, e := base64.RawURLEncoding.DecodeString(q.Get("addons"))
					if e != nil {
						t.Fatal(e)
					}
					reader, e := gzip.NewReader(bytes.NewReader(compressed))
					if e != nil {
						t.Fatal(e)
					}
					data, e := io.ReadAll(reader)
					reader.Close()
					if e != nil {
						t.Fatal(e)
					}
					var addons any
					_ = json.Unmarshal(data, &addons)
					body["addons"] = addons
				}
				if qr, ok := body["qrDataUrl"].(string); ok {
					body["qr"] = strings.HasPrefix(qr, "data:image/png;base64,")
					delete(body, "qrDataUrl")
				}
				values = append(values, map[string]any{"status": float64(res.StatusCode), "body": body})
			}
			p, e = os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			var stored any
			_ = json.Unmarshal(p, &stored)
			info, e := os.Stat(file)
			if e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			got := map[string]any{"values": values, "requests": requests, "raw": stored, "mode": float64(info.Mode().Perm())}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[i]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[i])
				t.Fatalf("actual factory/Source row%d\nGo%s\nSource%s", i, a, b)
			}
		})
	}
	t.Logf("%d original Feishu handler profiles via actual NewDesktopServer HTTP and private atomic storage", len(rows))
}

func TestDesktopFeishuServerCloseCancelsOwnedUpstreamWithoutSaving(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
		}
		if strings.HasPrefix(string(p), "action=begin") {
			io.WriteString(w, `{"device_code":"private-close","verification_uri_complete":"https://open.feishu.cn/x"}`)
			return
		}
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
		io.WriteString(w, `{"client_id":"must-not-save","client_secret":"must-not-save"}`)
	}))
	defer upstream.Close()
	defer close(release)
	destination, _ := url.Parse(upstream.URL)
	client := &http.Client{Transport: feishuFactoryRedirect{destination, upstream.Client().Transport}}
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(config, "adapters.json")
	seed := []byte(`{"future":17,"feishu":{"appSecret":"old-private"}}`)
	if e := os.WriteFile(file, seed, 0600); e != nil {
		t.Fatal(e)
	}
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, FeishuRegistrationHTTPClient: client, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	peer := httptest.NewServer(s)
	defer peer.Close()
	res, e := peer.Client().Post(peer.URL+"/api/adapters/feishu/registration/begin", "application/json", strings.NewReader(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	var begin map[string]any
	e = json.NewDecoder(res.Body).Decode(&begin)
	res.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	p, _ := json.Marshal(map[string]any{"sessionKey": begin["sessionKey"]})
	done := make(chan int, 1)
	go func() {
		res, e := peer.Client().Post(peer.URL+"/api/adapters/feishu/registration/poll", "application/json", bytes.NewReader(p))
		if e != nil {
			done <- 0
			return
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		done <- res.StatusCode
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("pending physical poll absent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = s.Close(ctx); e != nil {
		t.Fatal("real owner close", e)
	}
	select {
	case status := <-done:
		if status != 500 {
			t.Fatal("cancelled source response boundary", status)
		}
	case <-ctx.Done():
		t.Fatal("registration handler survived server close")
	}
	raw, e := os.ReadFile(file)
	if e != nil || string(raw) != string(seed) {
		t.Fatal("aborted upstream mutated private config", e)
	}
}
