package remoteio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceV1IngressOptimisticAppendConflictRecoveryAuthAndCompactQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, behavior := range []string{"success", "already_stored", "adopt_head", "refetch_head", "unauthorized", "rate_limit", "invalid_logs"} {
		t.Run(behavior, func(t *testing.T) {
			type peer struct {
				server     *httptest.Server
				mu         sync.Mutex
				trace      []protocol.JSONObject
				puts, gets int
			}
			newPeer := func() *peer {
				p := &peer{}
				p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					p.mu.Lock()
					defer p.mu.Unlock()
					row := protocol.NewJSONObject(protocol.JSONField{Name: "method", Value: quote(r.Method)}, protocol.JSONField{Name: "path", Value: quote(r.URL.RequestURI())}, protocol.JSONField{Name: "auth", Value: quote(r.Header.Get("Authorization"))}, protocol.JSONField{Name: "last", Value: quote(r.Header.Get("Last-Uuid"))})
					if len(body) > 0 {
						row.Set("body", body)
					}
					p.trace = append(p.trace, row)
					w.Header().Set("Content-Type", "application/json")
					if r.Method == "GET" {
						p.gets++
						if behavior == "invalid_logs" {
							_, _ = io.WriteString(w, `{"loglines":null}`)
							return
						}
						if p.gets == 1 {
							_, _ = io.WriteString(w, `{"loglines":[{"type":"user","uuid":"old-source-v1","future":{"opaque":true}}]}`)
						} else {
							_, _ = io.WriteString(w, `{"loglines":[{"type":"user","uuid":"new-server-head"},{"type":"summary","summary":"tail with no uuid"}]}`)
						}
						return
					}
					p.puts++
					if p.puts == 1 {
						switch behavior {
						case "already_stored":
							w.Header().Set("X-Last-Uuid", "source-entry-1")
							w.WriteHeader(409)
						case "adopt_head":
							w.Header().Set("X-Last-Uuid", "new-server-head")
							w.WriteHeader(409)
						case "refetch_head":
							w.WriteHeader(409)
						case "unauthorized":
							w.WriteHeader(401)
						case "rate_limit":
							w.WriteHeader(429)
						default:
							w.WriteHeader(201)
						}
					} else {
						w.WriteHeader(201)
					}
					_, _ = io.WriteString(w, `{}`)
				}))
				t.Cleanup(p.server.Close)
				return p
			}
			env := map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-v1-token", "CLAUDE_AFTER_LAST_COMPACT": "1"}
			messages := []protocol.JSONObject{object([]byte(`{"type":"user","uuid":"source-entry-1","message":{"role":"user","content":"actual v1 data"},"future":{"opaque":true}}`)), object([]byte(`{"type":"assistant","uuid":"source-entry-2","parentUuid":"source-entry-1","message":{"role":"assistant","content":"persisted after prior acknowledgement"}}`))}
			old := newPeer()
			want := runSourceFixture(t, "native-remoteio-http.fixture.ts", map[string]any{"legacy": true, "url": old.server.URL + "/ingress/private", "env": env, "messages": messages})
			p := newPeer()
			ingress := NewIngress(nil, NewIngressAuth(env), func() bool { return true })
			defer ingress.Close()
			logs, err := ingress.Read(ctx, "fixture-v1", p.server.URL+"/ingress/private")
			if err != nil {
				t.Fatal(err)
			}
			results := []bool{}
			for _, message := range messages {
				success, err := ingress.Append(ctx, "fixture-v1", p.server.URL+"/ingress/private", message)
				if err != nil {
					t.Fatal(err)
				}
				results = append(results, success)
			}
			for name, pair := range map[string][2]json.RawMessage{"logs": {want.Get("logs"), raw(logs)}, "results": {want.Get("results"), raw(results)}} {
				var a, b any
				_ = json.Unmarshal(pair[0], &a)
				_ = json.Unmarshal(pair[1], &b)
				if !reflect.DeepEqual(a, b) {
					t.Errorf("%s actual Source%s Go%s", name, pair[0], pair[1])
				}
			}
			old.mu.Lock()
			sourceTrace := append([]protocol.JSONObject(nil), old.trace...)
			old.mu.Unlock()
			p.mu.Lock()
			goTrace := append([]protocol.JSONObject(nil), p.trace...)
			p.mu.Unlock()
			var a, b any
			_ = json.Unmarshal(raw(sourceTrace), &a)
			_ = json.Unmarshal(raw(goTrace), &b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("actual v1 wire Source%s Go%s", raw(sourceTrace), raw(goTrace))
			}
		})
	}
}
