package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/fsnotify/fsnotify"
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

func TestDesktopWechatUnknownSessionUsesActualRegistrationFactory(t *testing.T) {
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
	response, err := peer.Client().Post(peer.URL+"/api/adapters/wechat/login/poll", "application/json", strings.NewReader(`{"sessionKey":"unknown-private"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), `"status":"not_started"`) {
		t.Fatalf("actual Wechat factory must return Source not_started: %d %s %v", response.StatusCode, body, err)
	}
}

// Actual factory consumer, original complete handler and exact persisted bytes.
type wechatFactoryReply struct {
	Status int    `json:"status,omitempty"`
	Body   any    `json:"body,omitempty"`
	Raw    string `json:"raw,omitempty"`
}
type wechatFactoryAction struct {
	FailWrite bool     `json:"failWrite,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	Raw       string   `json:"raw,omitempty"`
	Tail      []string `json:"tail,omitempty"`
	Method    string   `json:"method,omitempty"`
}
type wechatFactoryRow struct {
	Route   bool                  `json:"route"`
	Seed    any                   `json:"seed"`
	Actions []wechatFactoryAction `json:"actions"`
	Replies []wechatFactoryReply  `json:"replies"`
}
type wechatFactoryRedirect struct {
	destination *url.URL
	transport   http.RoundTripper
	observe     func(*http.Request)
}

func (f wechatFactoryRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "ilinkai.weixin.qq.com" {
		return nil, fmt.Errorf("unowned WeChat factory logical target")
	}
	if f.observe != nil {
		f.observe(req)
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = f.destination.Scheme
	u.Host = f.destination.Host
	clone.URL = &u
	return f.transport.RoundTrip(clone)
}
func newWechatFactoryTest(t *testing.T, client *http.Client, seed any) (*server.Server, string) {
	t.Helper()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(config, "adapters.json")
	raw, _ := json.Marshal(seed)
	if e := os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, WechatRegistrationHTTPClient: client, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	return s, file
}
func TestDesktopWechatOriginalFullHandlerHTTPErrorResponsesAndPersistedBytes(t *testing.T) {
	seed := map[string]any{"future": 17, "wechat": map[string]any{"accountId": "old-id", "botToken": "old-secret", "baseUrl": "https://old.test", "userId": "old-user", "allowedUsers": []string{"keep-allowed"}, "pairedUsers": []any{map[string]any{"id": "old-paired"}}, "future": true, "defaultWorkDir": "/private/work"}, "telegram": map[string]any{"botToken": "other-fake"}}
	qr := map[string]any{"qrcode": "private-qrcode", "qrcode_img_content": "https://private.test/qr"}
	confirmed := map[string]any{"status": "confirmed", "bot_token": "private-new-token", "ilink_bot_id": "private-new-id"}
	rows := []wechatFactoryRow{
		{true, seed, []wechatFactoryAction{{Tail: []string{"unbind"}, FailWrite: true}}, nil},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll", FailWrite: true}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Body: confirmed}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}, {Tail: []string{"unbind"}}, {Tail: []string{"unbind", "extra"}}}, []wechatFactoryReply{{Body: qr}, {Body: confirmed}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "start"}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Body: qr}, {Body: map[string]any{"status": "scaned"}}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Body: map[string]any{"status": "confirmed", "bot_token": "partial"}}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Raw: `{"status":"confirmed","bot_token":"\ud800","ilink_bot_id":"\ud801","ilink_user_id":"\ud802","baseurl":"https://new.test"}`}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Body: map[string]any{"status": "confirmed", "bot_token": "****", "ilink_bot_id": "new", "ilink_user_id": nil}}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Body: map[string]any{"status": "confirmed", "bot_token": 17, "ilink_bot_id": "new"}}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Body: map[string]any{"status": "confirmed", "bot_token": "new-token", "ilink_bot_id": "new", "baseurl": 17}}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Body: map[string]any{"status": "unknown"}}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Raw: "null"}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}}, []wechatFactoryReply{{Status: 500, Raw: "private error"}}},
		{true, seed, []wechatFactoryAction{{Kind: "start"}, {Kind: "poll"}, {Kind: "poll"}}, []wechatFactoryReply{{Body: qr}, {Raw: "invalid JSON"}, {Body: confirmed}}},
		{true, seed, []wechatFactoryAction{{Kind: "start", Tail: []string{"login", "start", "extra"}}, {Kind: "poll", Tail: []string{"login", "poll", "extra"}}}, []wechatFactoryReply{{Body: qr}, {Body: confirmed}}},
		{true, seed, []wechatFactoryAction{{Tail: []string{"unknown"}}, {Tail: []string{"login", "start"}, Method: "GET"}, {Tail: []string{"login", "cancel"}}}, nil},
	}
	for _, raw := range []string{"invalid JSON", "null", "[]", `{}`, `{"sessionKey":17}`, `{"sessionKey":""}`, `{"sessionKey":"   "}`, `{"sessionKey":"\ud800"}`, `{"sessionKey":"` + strings.Repeat("x", 257) + `"}`, `{"sessionKey":"` + strings.Repeat("😀", 128) + `"}`, `{"sessionKey":"` + strings.Repeat("😀", 129) + `"}`} {
		rows = append(rows, wechatFactoryRow{true, seed, []wechatFactoryAction{{Kind: "poll", Raw: raw}}, nil})
	}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	expected := runWechatFactorySource(t, input)
	if len(expected) != len(rows) {
		t.Fatal("Source row count")
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var mu sync.Mutex
			at := 0
			requests := []any{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				if at >= len(row.Replies) {
					mu.Unlock()
					t.Error("unexpected physical platform request")
					w.WriteHeader(500)
					return
				}
				reply := row.Replies[at]
				at++
				mu.Unlock()
				status := reply.Status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				if reply.Raw != "" {
					io.WriteString(w, reply.Raw)
				} else {
					raw, _ := json.Marshal(reply.Body)
					w.Write(raw)
				}
			}))
			defer upstream.Close()
			target, _ := url.Parse(upstream.URL)
			client := &http.Client{Transport: wechatFactoryRedirect{target, upstream.Client().Transport, func(req *http.Request) {
				_, timed := req.Context().Deadline()
				mu.Lock()
				requests = append(requests, map[string]any{"url": req.URL.String(), "method": req.Method, "appId": req.Header.Get("ilink-app-id"), "clientVersion": req.Header.Get("ilink-app-clientversion"), "timed": timed})
				mu.Unlock()
			}}}
			s, file := newWechatFactoryTest(t, client, row.Seed)
			peer := httptest.NewServer(s)
			defer peer.Close()
			key := "private-fixed-key"
			values := []any{}
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
				if a.FailWrite {
					if e := os.Chmod(filepath.Dir(file), 0500); e != nil {
						t.Fatal(e)
					}
					defer os.Chmod(filepath.Dir(file), 0700)
				}
				req, _ := http.NewRequest(method, peer.URL+"/api/adapters/wechat/"+strings.Join(tail, "/"), strings.NewReader(raw))
				res, e := peer.Client().Do(req)
				if e != nil {
					t.Fatal(e)
				}
				data, e := io.ReadAll(res.Body)
				res.Body.Close()
				if e != nil {
					t.Fatal(e)
				}
				var body map[string]any
				if e = json.Unmarshal(data, &body); e != nil {
					t.Fatal(e, string(data))
				}
				if v, ok := body["sessionKey"].(string); ok {
					key = v
					body["sessionKey"] = "<uuid>"
				}
				values = append(values, map[string]any{"status": float64(res.StatusCode), "body": body})
			}
			raw, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			var stored any
			if e = json.Unmarshal(raw, &stored); e != nil {
				t.Fatal(e)
			}
			info, e := os.Stat(file)
			if e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			got := map[string]any{"values": values, "requests": requests, "wireCalls": float64(at), "raw": stored, "storedJson": string(raw), "mode": float64(info.Mode().Perm())}
			mu.Unlock()
			if !reflect.DeepEqual(got, expected[i]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(expected[i])
				t.Fatalf("original handler/actual factory row%d\nGo%s\nSource%s", i, a, b)
			}
		})
	}
	t.Logf("%d original handler profiles, actual factory/private HTTP/physical exact atomic bytes", len(rows))
}
func runWechatFactorySource(t *testing.T, input []byte) []map[string]any {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	root := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-wechat-registration.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "TMPDIR=" + root, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(root), "CC_HAHA_WECHAT_REGISTRATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var result []map[string]any
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatal(e, string(out))
	}
	return result
}
func TestDesktopWechatRendererDisconnectContinuesOriginalBusinessAndServerCloseCancels(t *testing.T) {
	seed := map[string]any{"future": 17, "wechat": map[string]any{"accountId": "old-id", "botToken": "old-secret", "userId": "old-user", "allowedUsers": []string{"retained"}, "pairedUsers": []any{map[string]any{"id": "old"}}}}
	// The original complete handler ignores its Request.signal once poll begins.
	input, _ := json.Marshal(map[string]any{"rows": []any{map[string]any{"route": true, "seed": seed, "actions": []any{map[string]any{"kind": "start"}, map[string]any{"kind": "cancel_renderer_poll"}}, "replies": []any{map[string]any{"body": map[string]any{"qrcode": "private", "qrcode_img_content": "https://private.test/qr"}}, map[string]any{"hold": true, "body": map[string]any{"status": "confirmed", "bot_token": "new-private-token", "ilink_bot_id": "new-private-id"}}}}}})
	source := runWechatFactorySource(t, input)
	if len(source) != 1 {
		t.Fatal("Source cancellation row")
	}
	for _, closeServer := range []bool{false, true} {
		for _, partialBody := range []bool{false, true} {
			t.Run(fmt.Sprint(closeServer, partialBody), func(t *testing.T) {
				started := make(chan struct{}, 1)
				released := make(chan struct{})
				var once sync.Once
				defer once.Do(func() { close(released) })
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/ilink/bot/get_bot_qrcode" {
						io.WriteString(w, `{"qrcode":"private","qrcode_img_content":"https://private.test/qr"}`)
						return
					}
					if r.URL.Query().Get("qrcode") != "private" {
						t.Error("actual QR payload lost")
					}
					if partialBody {
						io.WriteString(w, `{"status":`)
						w.(http.Flusher).Flush()
					}
					started <- struct{}{}
					select {
					case <-r.Context().Done():
						return
					case <-released:
					}
					if partialBody {
						io.WriteString(w, `"confirmed","bot_token":"new-private-token","ilink_bot_id":"new-private-id"}`)
					} else {
						io.WriteString(w, `{"status":"confirmed","bot_token":"new-private-token","ilink_bot_id":"new-private-id"}`)
					}
				}))
				defer upstream.Close()
				defer once.Do(func() { close(released) })
				target, _ := url.Parse(upstream.URL)
				client := &http.Client{Transport: wechatFactoryRedirect{target, upstream.Client().Transport, func(req *http.Request) {
					deadline, ok := req.Context().Deadline()
					if req.URL.Path == "/ilink/bot/get_bot_qrcode" {
						if ok {
							t.Error("start has an explicit timeout")
						}
					} else if !ok || time.Until(deadline) > 35*time.Second || time.Until(deadline) < 34*time.Second {
						t.Error("Source poll 35-second deadline missing")
					}
				}}}
				s, file := newWechatFactoryTest(t, client, seed)
				before, _ := os.ReadFile(file)
				watcher, e := fsnotify.NewWatcher()
				if e != nil {
					t.Fatal(e)
				}
				defer watcher.Close()
				if e = watcher.Add(filepath.Dir(file)); e != nil {
					t.Fatal(e)
				}
				handled := make(chan struct{}, 1)
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/poll") {
						defer func() { handled <- struct{}{} }()
					}
					s.ServeHTTP(w, r)
				}))
				defer peer.Close()
				res, e := peer.Client().Post(peer.URL+"/api/adapters/wechat/login/start", "application/json", strings.NewReader("ignored invalid JSON"))
				if e != nil {
					t.Fatal(e)
				}
				var began map[string]any
				e = json.NewDecoder(res.Body).Decode(&began)
				res.Body.Close()
				if e != nil || res.StatusCode != 200 {
					t.Fatal("actual start", e, began)
				}
				raw, _ := json.Marshal(map[string]any{"sessionKey": began["sessionKey"]})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				req, _ := http.NewRequestWithContext(ctx, "POST", peer.URL+"/api/adapters/wechat/login/poll", bytes.NewReader(raw))
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
					t.Fatal("private platform poll was not submitted")
				}
				if closeServer {
					shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					if e = s.Close(shutdown); e != nil {
						t.Fatal(e)
					}
				} else {
					cancel()
					select {
					case e = <-done:
						if e == nil {
							t.Fatal("actual renderer TCP request did not cancel")
						}
					case <-time.After(3 * time.Second):
						t.Fatal("renderer disconnect not observed")
					}
					once.Do(func() { close(released) })
				}
				select {
				case <-handled:
				case <-time.After(3 * time.Second):
					t.Fatal("actual server handler not completed/joined")
				}
				if closeServer {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Fatal("Close left renderer request held")
					}
				}
				if !closeServer {
					timer := time.NewTimer(5 * time.Second)
					defer timer.Stop()
					committed := false
					for !committed {
						select {
						case event := <-watcher.Events:
							if filepath.Base(event.Name) == filepath.Base(file) {
								data, err := os.ReadFile(file)
								committed = err == nil && string(data) == source[0]["storedJson"].(string)
							}
						case err := <-watcher.Errors:
							t.Fatal(err)
						case <-timer.C:
							t.Fatal("committed business did not publish actual adapter config after response499")
						}
					}
				}
				after, e := os.ReadFile(file)
				if e != nil {
					t.Fatal(e)
				}
				if closeServer {
					if !bytes.Equal(before, after) {
						t.Fatal("ServerClose wrote credentials from canceled pending business")
					}
				} else {
					if string(after) != source[0]["storedJson"].(string) {
						t.Fatalf("renderer disconnect aborted original submitted business\nGo%s\nSource%s", after, source[0]["storedJson"])
					}
				}
			})
		}
	}
}
