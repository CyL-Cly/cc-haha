package imruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

func wechatEdgeFailure(err error) string {
	if err == nil {
		return ""
	}
	detail := err.Error()
	for _, word := range []string{"slice", "trim", "iterable", "null"} {
		if strings.Contains(detail, word) {
			return word
		}
	}
	return "other"
}
func wechatEdgeSeed(work string) map[string]any {
	return map[string]any{"defaultProjectDir": work, "wechat": map[string]any{"accountId": "private-account", "botToken": "private-token", "allowedUsers": []string{}}, "pairing": map[string]any{"code": "ABCDEF", "expiresAt": int64(1770000010000), "createdAt": int64(1770000000000)}}
}
func wechatEdgeConfig(t *testing.T, home, work string, seed any) string {
	t.Helper()
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), raw(seed), 0600); err != nil {
		t.Fatal(err)
	}
	return config
}
func wechatEdgeSnapshot(r *WechatRuntime, notices []any) map[string]any {
	r.Dedup.mu.Lock()
	keys := []string{}
	for _, key := range r.Dedup.keys {
		var text string
		_ = json.Unmarshal([]byte(key), &text)
		keys = append(keys, text)
	}
	r.Dedup.mu.Unlock()
	r.Contexts.mu.Lock()
	names := []string{}
	for id := range r.Contexts.tokens {
		names = append(names, id)
	}
	sort.Strings(names)
	contexts := []any{}
	for _, id := range names {
		contexts = append(contexts, []any{id, append(json.RawMessage(nil), r.Contexts.tokens[id]...)})
	}
	r.Contexts.mu.Unlock()
	return map[string]any{"dedup": keys, "contexts": contexts, "notices": append([]any{}, notices...), "packets": []any{}}
}
func wechatEdgeRuntime(t *testing.T, home, work, config string, handler http.Handler, sleep func(context.Context, time.Duration) error) (*WechatRuntime, *httptest.Server, *sync.Mutex, *[]any) {
	t.Helper()
	mu := &sync.Mutex{}
	notices := &[]any{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/ilink/bot/sendmessage" {
			var body protocol.JSONObject
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			message := object(body.Get("msg"))
			var items []protocol.JSONObject
			_ = json.Unmarshal(message.Get("item_list"), &items)
			notice := map[string]any{"id": message.Get("to_user_id"), "text": object(items[0].Get("text_item")).Get("text")}
			if token := message.Get("context_token"); len(token) > 0 {
				notice["context"] = token
			}
			mu.Lock()
			*notices = append(*notices, notice)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ret":0}`))
			return
		}
		if handler != nil {
			handler.ServeHTTP(w, req)
			return
		}
		_, _ = w.Write([]byte(`{"ret":0}`))
	}))
	cfg := LoadPlatformConfig("wechat", home, work, map[string]string{"CLAUDE_CONFIG_DIR": config, "WECHAT_BASE_URL": peer.URL, "ADAPTER_SERVER_URL": "ws" + strings.TrimPrefix(peer.URL, "http")}, func(string) {})
	runtime, err := NewWechatRuntime(context.Background(), WechatRuntimeOptions{Config: cfg, ServerHTTP: peer.Client(), Now: func() time.Time { return time.UnixMilli(1770000000000) }, Sleep: sleep})
	if err != nil {
		peer.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close(); peer.Close() })
	return runtime, peer, mu, notices
}
func TestActualWechatConsumerSourceDedupContextBeforeEmptyMalformedAndExternalPairing(t *testing.T) {
	profiles := []struct {
		name     string
		messages []string
	}{
		{"null-whitespace-and-falsy-sender", []string{`null`, "  null \n", `{"from_user_id":false,"message_id":"false"}`, `{"from_user_id":0,"message_id":"zero"}`, `{"from_user_id":"","message_id":"blank"}`, `false`, `"primitive"`, `[]`}},
		{"global-key-before-context", []string{`{"from_user_id":"U1","message_id":"same","context_token":"ctx1","item_list":[]}`, `{"from_user_id":"U2","message_id":"same","context_token":"ctx2","item_list":[{"type":1,"text_item":{"text":"/help"}}]}`, `{"from_user_id":"U1","message_id":"fresh","context_token":false,"item_list":[]}`}},
		{"malformed-text-consumes-key", []string{`{"from_user_id":"U1","message_id":"bad","context_token":{"n":1},"item_list":[{"type":3,"voice_item":{"text":7}}]}`, `{"from_user_id":"U1","message_id":"bad","context_token":"replacement","item_list":[{"type":1,"text_item":{"text":"/help"}}]}`}},
		{"numeric-empty-consumes-key", []string{`{"from_user_id":7,"message_id":"empty-number","item_list":[]}`, `{"from_user_id":"U1","message_id":"empty-number","item_list":[{"type":1,"text_item":{"text":"/help"}}]}`}},
		{"numeric-text-consumes-key-before-redaction-error", []string{`{"from_user_id":7,"message_id":"number","item_list":[{"type":1,"text_item":{"text":"hello"}}]}`, `{"from_user_id":"U1","message_id":"number","item_list":[{"type":1,"text_item":{"text":"/help"}}]}`}},
		{"nullish-identity-coercion", []string{`{"from_user_id":"U1","context_token":"first","item_list":[]}`, `{"from_user_id":"U2","message_id":null,"seq":null,"create_time_ms":null,"context_token":"second","item_list":[]}`, `{"from_user_id":"U1","message_id":{},"seq":[],"create_time_ms":false,"context_token":"third","item_list":[]}`}},
		{"external-pairing-one-time", []string{`{"from_user_id":"U1","message_id":"pair","context_token":"ctx","item_list":[{"type":1,"text_item":{"text":" abcdef "}}]}`, `{"from_user_id":"U2","message_id":"second-pair","item_list":[{"type":1,"text_item":{"text":"ABCDEF"}}]}`}},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			home := t.TempDir()
			sourceHome := t.TempDir()
			work := filepath.Join(home, "repo")
			if err := os.MkdirAll(work, 0700); err != nil {
				t.Fatal(err)
			}
			seed := wechatEdgeSeed(work)
			actions := []map[string]any{}
			for _, message := range profile.messages {
				actions = append(actions, map[string]any{"raw": message})
			}
			want := runPlatformStageSource(t, "native-wechat-runtime-edge.fixture.ts", "CC_HAHA_WECHAT_RUNTIME_EDGE_FIXTURE", map[string]any{"mode": "route", "config": seed, "work": work, "now": int64(1770000000000), "users": []string{"U1", "U2"}, "actions": actions}, sourceHome)
			config := wechatEdgeConfig(t, home, work, seed)
			runtime, _, mu, notices := wechatEdgeRuntime(t, home, work, config, nil, nil)
			rows := []any{}
			for _, message := range profile.messages {
				err := runtime.HandleMessage(json.RawMessage(message))
				runtime.Chat.wg.Wait()
				mu.Lock()
				snapshot := wechatEdgeSnapshot(runtime, *notices)
				mu.Unlock()
				snapshot["error"] = wechatEdgeFailure(err)
				rows = append(rows, snapshot)
			}
			stored, err := os.ReadFile(filepath.Join(config, "adapters.json"))
			if err != nil {
				t.Fatal(err)
			}
			got := protocol.StringifyValidatedJavaScriptJSON(raw(map[string]any{"rows": rows, "config": json.RawMessage(stored)}))
			exactJSON(t, got, want, "actual Source admission side effects")
		})
	}
}
func TestActualWechatConsumerSourcePollCursorPrecedesErrorsAndMalformedBatches(t *testing.T) {
	responses := []string{
		`{"get_updates_buf":"cursor-error","ret":9,"msgs":[{"from_user_id":"U1","message_id":"must-not-admit","item_list":[]}]}`,
		`{"get_updates_buf":0,"ret":0,"msgs":"x🧡"}`,
		`{"get_updates_buf":{"nested":1},"ret":"9","msgs":[]}`,
		`{"get_updates_buf":[],"errcode":-0,"msgs":null}`,
		`{"get_updates_buf":"cursor-inf","errcode":1e400,"msgs":[]}`,
		`null`,
		" \nnull \t",
		`{"get_updates_buf":"cursor-object","msgs":{"not":"iterable"}}`,
		`{"get_updates_buf":false,"msgs":[null,{"from_user_id":"U1","message_id":"after-null","item_list":[]}]}`,
		`{"get_updates_buf":"cursor-voice","msgs":[{"from_user_id":"U1","message_id":"voice","context_token":"kept-before-error","item_list":[{"type":3,"voice_item":{"text":true}}]},{"from_user_id":"U1","message_id":"after-voice","item_list":[]}]}`,
		`not-json`,
		`{"get_updates_buf":false,"ret":false,"errcode":"5","msgs":[]}`,
		`{"get_updates_buf":"last","msgs":[{"from_user_id":"U1","message_id":"empty-final","context_token":"latest","item_list":[]}]}`,
	}
	home := t.TempDir()
	sourceHome := t.TempDir()
	work := filepath.Join(home, "repo")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	seed := wechatEdgeSeed(work)
	want := runPlatformStageSource(t, "native-wechat-runtime-edge.fixture.ts", "CC_HAHA_WECHAT_RUNTIME_EDGE_FIXTURE", map[string]any{"mode": "poll", "config": seed, "work": work, "now": int64(1770000000000), "users": []string{"U1"}, "responses": responses}, sourceHome)
	var runtime *WechatRuntime
	var mu sync.Mutex
	polls := []json.RawMessage{}
	retries := []int64{}
	index := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/ilink/bot/getupdates" {
			t.Errorf("unexpected endpoint: %s", req.URL.Path)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		data, _ := io.ReadAll(req.Body)
		body := object(data)
		mu.Lock()
		polls = append(polls, append(json.RawMessage(nil), body.Get("get_updates_buf")...))
		i := index
		index++
		mu.Unlock()
		if i == len(responses) {
			runtime.cancel()
			<-req.Context().Done()
			return
		}
		_, _ = w.Write([]byte(responses[i]))
	})
	config := wechatEdgeConfig(t, home, work, seed)
	runtime, _, noticeMu, notices := wechatEdgeRuntime(t, home, work, config, handler, func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		retries = append(retries, d.Milliseconds())
		mu.Unlock()
		return ctx.Err()
	})
	runtime.Start()
	done := make(chan struct{})
	go func() { _ = runtime.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("finite poll fixture did not finish")
	}
	runtime.Chat.wg.Wait()
	noticeMu.Lock()
	snapshot := wechatEdgeSnapshot(runtime, *notices)
	noticeMu.Unlock()
	mu.Lock()
	snapshot["polls"] = polls
	snapshot["retries"] = retries
	mu.Unlock()
	snapshot["cursor"] = runtime.Cursor()
	exactJSON(t, protocol.StringifyValidatedJavaScriptJSON(raw(snapshot)), want, "actual Source polling cursor/error batch ordering")
}

// The download barrier is released by an explicit control request, so this
// compares actual Source queue admission rather than scheduler delays.
func TestActualWechatConsumerSourceFIFOAuthorizationSnapshotAndDeferredMedia(t *testing.T) {
	for _, cancelPending := range []bool{false, true} {
		name := "drain"
		if cancelPending {
			name = "cancel-original-sigint"
		}
		t.Run(name, func(t *testing.T) {
			home, sourceHome := t.TempDir(), t.TempDir()
			work := filepath.Join(home, "repo")
			if err := os.MkdirAll(work, 0700); err != nil {
				t.Fatal(err)
			}
			type journal struct {
				sync.Mutex
				downloads []string
				packets   []any
			}
			run := func(source bool) json.RawMessage {
				j := &journal{downloads: []string{}, packets: []any{}}
				started, release, third := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var startOnce, releaseOnce, thirdOnce sync.Once
				ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
					defer conn.Close()
					for {
						var m protocol.JSONObject
						if websocket.JSON.Receive(conn, &m) != nil {
							return
						}
						j.Lock()
						j.packets = append(j.packets, m)
						n := len(j.packets)
						j.Unlock()
						if n == 3 {
							thirdOnce.Do(func() { close(third) })
						}
					}
				}}
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if strings.HasPrefix(req.URL.Path, "/ws/") {
						ws.ServeHTTP(w, req)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					switch req.URL.Path {
					case "/media/first", "/media/third", "/media/never":
						tag := strings.TrimPrefix(req.URL.Path, "/media/")
						j.Lock()
						j.downloads = append(j.downloads, tag)
						j.Unlock()
						if tag == "first" {
							startOnce.Do(func() { close(started) })
							select {
							case <-release:
							case <-req.Context().Done():
								return
							}
						}
						_, _ = w.Write([]byte("private-image-" + tag))
					case "/control/started":
						select {
						case <-started:
							_, _ = w.Write([]byte(`{}`))
						case <-req.Context().Done():
						}
					case "/control/release":
						releaseOnce.Do(func() { close(release) })
						_, _ = w.Write([]byte(`{}`))
					case "/control/snapshot":
						j.Lock()
						data := raw(map[string]any{"downloads": append([]string{}, j.downloads...)})
						j.Unlock()
						_, _ = w.Write(data)
					default:
						_, _ = io.Copy(io.Discard, req.Body)
						_, _ = w.Write([]byte(`{"ret":0}`))
					}
				}))
				defer peer.Close()
				defer releaseOnce.Do(func() { close(release) })
				seed := wechatEdgeSeed(work)
				allowed := wechatEdgeSeed(work)
				allowed["wechat"].(map[string]any)["allowedUsers"] = []string{"U1"}
				image := func(key, tag string) string {
					return string(raw(map[string]any{"from_user_id": "U1", "message_id": key, "context_token": "ctx-" + key, "item_list": []any{map[string]any{"type": 2, "msg_id": key, "image_item": map[string]any{"media": map[string]string{"full_url": peer.URL + "/media/" + tag}}}}}))
				}
				unauthorized := image("unauthorized", "never")
				messages := []string{image("first", "first"), `{"from_user_id":"U1","message_id":"second","context_token":"ctx-second","item_list":[{"type":1,"text_item":{"text":"second text"}}]}`, image("third", "third")}
				if source {
					mode := "fifo"
					if cancelPending {
						mode = "cancel"
					}
					return runPlatformStageSource(t, "native-wechat-runtime-edge.fixture.ts", "CC_HAHA_WECHAT_RUNTIME_EDGE_FIXTURE", map[string]any{"mode": mode, "config": seed, "allowedConfig": allowed, "work": work, "now": int64(1770000000000), "peer": peer.URL, "unauthorized": unauthorized, "messages": messages}, sourceHome)
				}
				config := wechatEdgeConfig(t, home, work, seed)
				cfg := LoadPlatformConfig("wechat", home, work, map[string]string{"CLAUDE_CONFIG_DIR": config, "WECHAT_BASE_URL": peer.URL, "ADAPTER_SERVER_URL": "ws" + strings.TrimPrefix(peer.URL, "http")}, func(string) {})
				runtime, err := NewWechatRuntime(context.Background(), WechatRuntimeOptions{Config: cfg, ServerHTTP: peer.Client(), Now: func() time.Time { return time.UnixMilli(1770000000000) }})
				if err != nil {
					t.Fatal(err)
				}
				defer runtime.Close()
				files := func() []any {
					dir := filepath.Join(home, ".claude", "im-downloads")
					values := []any{}
					err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
						if err != nil {
							if os.IsNotExist(err) {
								return nil
							}
							return err
						}
						if entry.IsDir() {
							return nil
						}
						bytes, err := os.ReadFile(path)
						if err != nil {
							return err
						}
						name, err := filepath.Rel(dir, path)
						if err != nil {
							return err
						}
						values = append(values, map[string]string{"name": filepath.ToSlash(name), "bytes": base64.StdEncoding.EncodeToString(bytes)})
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
					return values
				}
				snapshot := func() map[string]any {
					j.Lock()
					defer j.Unlock()
					return map[string]any{"downloads": append([]string{}, j.downloads...), "packets": append([]any{}, j.packets...), "files": files()}
				}
				if err := runtime.HandleMessage(json.RawMessage(unauthorized)); err != nil {
					t.Fatal(err)
				}
				unauthorizedSnapshot := snapshot()
				if err := os.WriteFile(filepath.Join(config, "adapters.json"), raw(allowed), 0600); err != nil {
					t.Fatal(err)
				}
				if err := runtime.Store.Set("U1", "session-edge", work); err != nil {
					t.Fatal(err)
				}
				runtime.Bridge.Connect("U1", "session-edge", runtime.Chat.handler("U1"))
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if !runtime.Bridge.WaitOpen(ctx, "U1") {
					t.Fatal("physical websocket did not open")
				}
				if err := runtime.HandleMessage(json.RawMessage(messages[0])); err != nil {
					t.Fatal(err)
				}
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("first deferred download did not start")
				}
				for _, message := range messages[1:] {
					if err := runtime.HandleMessage(json.RawMessage(message)); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(config, "adapters.json"), raw(seed), 0600); err != nil {
					t.Fatal(err)
				}
				during := snapshot()
				if cancelPending {
					runtime.Close()
					after := snapshot()
					exactJSON(t, raw(after), raw(during), "Close cancels held media and queued work without new files/downloads/frames")
					// A post-close authorized admission cannot send a new model-facing frame.
					if err := os.WriteFile(filepath.Join(config, "adapters.json"), raw(allowed), 0600); err != nil {
						t.Fatal(err)
					}
					_ = runtime.HandleMessage(json.RawMessage(`{"from_user_id":"U1","message_id":"after-close","item_list":[{"type":1,"text_item":{"text":"must not send"}}]}`))
					runtime.Chat.wg.Wait()
					exactJSON(t, raw(snapshot()), raw(after), "closed runtime cannot start new queued work")
					return raw(map[string]any{"unauthorized": unauthorizedSnapshot, "during": during})
				}
				releaseOnce.Do(func() { close(release) })
				drained := make(chan struct{})
				go func() { runtime.Chat.wg.Wait(); close(drained) }()
				select {
				case <-drained:
				case <-ctx.Done():
					t.Fatal("FIFO queue did not drain")
				}
				select {
				case <-third:
				case <-ctx.Done():
					t.Fatal("physical consumer did not receive exactly three admitted frames")
				}
				return raw(map[string]any{"unauthorized": unauthorizedSnapshot, "during": during, "after": snapshot()})
			}
			want := run(true)
			got := run(false)
			exactJSON(t, got, want, "actual Source authorization snapshot/FIFO/deferred media and physical files")
		})
	}
}
