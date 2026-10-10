package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
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
)

func TestDesktopWecomUnknownSessionUsesActualRegistrationFactory(t *testing.T) {
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
	response, err := peer.Client().Post(peer.URL+"/api/adapters/wecom/login/poll", "application/json", strings.NewReader(`{"sessionKey":"unknown-private"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), `"status":"not_started"`) {
		t.Fatalf("actual Wecom factory must return Source not_started: %d %s %v", response.StatusCode, body, err)
	}
}

type wecomFactoryRedirect struct {
	destination *url.URL
	transport   http.RoundTripper
}

func (f wecomFactoryRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "work.weixin.qq.com" || (req.URL.Path != "/ai/qc/generate" && req.URL.Path != "/ai/qc/query_result") {
		return nil, fmt.Errorf("unowned WeCom logical target")
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = f.destination.Scheme
	u.Host = f.destination.Host
	clone.URL = &u
	clone.Header.Set("X-Fixture-Logical-URL", req.URL.String())
	return f.transport.RoundTrip(clone)
}
func TestDesktopWecomRoutesMatchActualSourceAndPhysicalPrivateStorage(t *testing.T) {
	seed := map[string]any{"future": 17, "wecom": map[string]any{"botId": "old-id", "secret": "old-secret", "allowedUsers": []string{"corp-allowed"}, "pairedUsers": []any{map[string]any{"id": "old-user"}}, "workspace": "retained", "future": true}, "telegram": map[string]any{"botToken": "other-fake"}}
	started := map[string]any{"data": map[string]any{"scode": " private-scode ", "auth_url": "https://work.weixin.qq.com/confirm?scode=private"}}
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
		{true, seed, []action{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}, {Tail: []string{"unbind"}}, {Tail: []string{"unbind", "extra"}}}, []reply{{Body: started}, {Body: map[string]any{"data": map[string]any{"status": " SUCCESS ", "bot_info": map[string]any{"botid": " new-id ", "secret": " new-secret "}}}}}},
		{true, seed, []action{{Kind: "start"}, {Kind: "cancel"}, {Kind: "poll"}}, []reply{{Body: started}}},
		{true, seed, []action{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, []reply{{Body: started}, {Body: map[string]any{"data": map[string]any{"status": "scanned"}}}, {Body: map[string]any{"data": map[string]any{"status": "timeout"}}}}},
		{true, seed, []action{{Kind: "start"}, {Kind: "poll"}}, []reply{{Body: started}, {Raw: "null"}}},
		{true, seed, []action{{Kind: "start"}}, []reply{{Body: map[string]any{"data": map[string]any{"scode": "private", "auth_url": "https://evil.test/x"}}}}},
		{true, seed, []action{{Tail: []string{"unknown"}}, {Tail: []string{"login", "start"}, Method: "GET"}}, nil},
	}

	rows = append(rows, row{true, seed, []action{{Kind: "start"}, {Kind: "poll"}}, []reply{{Body: started}, {Raw: `{"data":{"status":"success","bot_info":{"botid":" \ud800 ","secret":" \ud801 "}}}`}}})

	for _, raw := range []string{"not JSON", "null", "[]", `{}`, `{"sessionKey":17}`, `{"sessionKey":""}`, `{"sessionKey":"   "}`, `{"sessionKey":"\ud800"}`, `{"sessionKey":"\ud801"}`, `{"sessionKey":"` + strings.Repeat("x", 257) + `"}`, `{"sessionKey":"` + strings.Repeat("😀", 128) + `"}`, `{"sessionKey":"` + strings.Repeat("😀", 129) + `"}`} {
		rows = append(rows, row{true, seed, []action{{Kind: "poll", Raw: raw}, {Kind: "cancel", Raw: raw}}, nil})
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	private := t.TempDir()
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-wecom-registration.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = private
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "TMPDIR=" + private, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(private), "CC_HAHA_WECOM_REGISTRATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
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
				mu.Lock()
				requests = append(requests, map[string]any{"url": r.Header.Get("X-Fixture-Logical-URL"), "method": r.Method, "accept": r.Header.Get("Accept")})
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
			client := &http.Client{Transport: wecomFactoryRedirect{destination, upstream.Client().Transport}}
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
			s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, WecomRegistrationHTTPClient: client, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
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
					tail = []string{"login", a.Kind}
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
				req, _ := http.NewRequest(method, peer.URL+"/api/adapters/wecom/"+strings.Join(tail, "/"), strings.NewReader(raw))
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
				if expiry, ok := body["expiresAt"].(float64); ok {
					if delta := expiry - float64(time.Now().UnixMilli()); delta < 298000 || delta > 300000 {
						t.Fatal("actual five-minute expiry", delta)
					}
					body["expiresAt"] = "<five-minute-expiry>"
				}
				if v, ok := body["sessionKey"].(string); ok {
					key = v
					body["sessionKey"] = "<uuid>"
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
			normalized, e := protocol.ParseAndStringifyJavaScriptJSON(p)
			if e != nil {
				t.Fatal(e)
			}
			got := map[string]any{"values": values, "requests": requests, "raw": stored, "mode": float64(info.Mode().Perm()), "storedJson": string(normalized)}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[i]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[i])
				t.Fatalf("actual factory/Source row%d\nGo%s\nSource%s", i, a, b)
			}
		})
	}
	t.Logf("%d original Wecom handler profiles via actual NewDesktopServer HTTP and private atomic storage", len(rows))
}

func TestDesktopWecomInFlightCancelDisconnectSuccessAndServerClose(t *testing.T) {
	for _, closeServer := range []bool{false, true} {
		t.Run(fmt.Sprint(closeServer), func(t *testing.T) {
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ai/qc/generate" {
					io.WriteString(w, `{"data":{"scode":"private-scode","auth_url":"https://work.weixin.qq.com/x"}}`)
					return
				}
				if r.URL.Query().Get("scode") != "private-scode" {
					t.Error("actual scode query", r.URL)
				}
				started <- struct{}{}
				select {
				case <-r.Context().Done():
				case <-release:
				}
				io.WriteString(w, `{"data":{"status":"success","bot_info":{"botid":" new-bot ","secret":" new-secret "}}}`)
			}))
			defer upstream.Close()
			defer once.Do(func() { close(release) })
			destination, _ := url.Parse(upstream.URL)
			client := &http.Client{Transport: wecomFactoryRedirect{destination, upstream.Client().Transport}}
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
			if e := os.MkdirAll(config, 0700); e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(config, "adapters.json")
			seed := []byte(`{"future":17,"wecom":{"botId":"old-bot","secret":"old-secret","allowedUsers":["corp-user"],"pairedUsers":[{"id":"old-user"}],"workspace":"retained"}}`)
			if e := os.WriteFile(file, seed, 0600); e != nil {
				t.Fatal(e)
			}
			s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, WecomRegistrationHTTPClient: client, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
				return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			}})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			peer := httptest.NewServer(s)
			defer peer.Close()
			res, e := peer.Client().Post(peer.URL+"/api/adapters/wecom/login/start", "application/json", strings.NewReader("ignored invalid JSON"))
			if e != nil {
				t.Fatal(e)
			}
			var begin map[string]any
			e = json.NewDecoder(res.Body).Decode(&begin)
			res.Body.Close()
			if e != nil || res.StatusCode != 200 {
				t.Fatal("actual start", begin, e)
			}
			p, _ := json.Marshal(map[string]any{"sessionKey": begin["sessionKey"]})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", peer.URL+"/api/adapters/wecom/login/poll", bytes.NewReader(p))
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
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("actual poll absent")
			}
			if closeServer {
				shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if e = s.Close(shutdown); e != nil {
					t.Fatal("actual server close", e)
				}
			} else {
				cancel()
				select {
				case e := <-done:
					if e == nil {
						t.Fatal("renderer abort did not settle")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("renderer response retained")
				}
				res, e := peer.Client().Post(peer.URL+"/api/adapters/wecom/login/cancel", "application/json", bytes.NewReader(p))
				if e != nil {
					t.Fatal(e)
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if res.StatusCode != 200 {
					t.Fatal("cancel", res.StatusCode)
				}
			}
			once.Do(func() { close(release) })
			if closeServer {
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("handler survived server shutdown")
				}
				raw, e := os.ReadFile(file)
				if e != nil || string(raw) != string(seed) {
					t.Fatal("closed upstream changed private config", e)
				}
			} else {
				deadline := time.After(3 * time.Second)
				for {
					raw, e := os.ReadFile(file)
					if e == nil && strings.Contains(string(raw), `"new-secret"`) {
						var value map[string]any
						_ = json.Unmarshal(raw, &value)
						section := value["wecom"].(map[string]any)
						if section["botId"] != "new-bot" || len(section["pairedUsers"].([]any)) != 0 || !reflect.DeepEqual(section["allowedUsers"], []any{"corp-user"}) || section["workspace"] != "retained" || value["future"] != float64(17) {
							t.Fatal("success cleanup/allowlist", value)
						}
						break
					}
					select {
					case <-deadline:
						t.Fatal("in-flight success discarded after renderer/cancel")
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
		})
	}
}
