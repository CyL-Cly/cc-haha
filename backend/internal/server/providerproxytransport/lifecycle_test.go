package providerproxytransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func TestActualProxyHeadersIdleAndCancelAgainstOriginalProducers(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{{"mode": "trickle", "stream": true}, {"mode": "idle", "stream": true}, {"mode": "cancel", "stream": true}}
	home := t.TempDir()
	fixturePath, _ := filepath.Abs("../../../../scripts/quality-gate/go-parity/native-providerproxy-lifecycle.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(encoded(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source timeout/idle %v %s", e, stderr.String())
	}
	var expected []struct {
		Text  string
		Error *struct{ Name, Message string }
	}
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal(string(output))
	}
	for i, row := range rows {
		t.Run(row["mode"].(string), func(t *testing.T) {
			closed := make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				w.WriteHeader(200)
				_, _ = io.WriteString(w, "prefix")
				w.(http.Flusher).Flush()
				if row["mode"] == "trickle" {
					for index := 1; index <= 5; index++ {
						select {
						case <-time.After(20 * time.Millisecond):
							_, _ = fmt.Fprint(w, index)
							w.(http.Flusher).Flush()
						case <-r.Context().Done():
							return
						}
					}
					return
				}
				<-r.Context().Done()
			}))
			defer peer.Close()
			config := providerproxy.ProxyConfig{ID: "fixture", APIFormat: "anthropic", APIKey: "fixture", AuthStrategy: "auth_token", BaseURL: peer.URL, SupportsNestedToolResultMedia: false}
			service, e := New(Options{Provider: func(context.Context, string) (*providerproxy.ProxyConfig, error) { return &config, nil }, Settings: func(context.Context) (protocol.JSONObject, error) {
				return object(json.RawMessage(`{"network":{"proxy":{"mode":"direct"}}}`)), nil
			}, Environment: func() map[string]string { return map[string]string{} }, TimeoutOverride: 70 * time.Millisecond})
			if e != nil {
				t.Fatal(e)
			}
			request, _ := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1/proxy/v1/messages", strings.NewReader(`{"model":"fixture","stream":true,"messages":[]}`))
			response, e := service.Handle(request)
			if e != nil {
				t.Fatal(e)
			}
			var text strings.Builder
			buffer := make([]byte, 64)
			var failure error
			for {
				n, e := response.Body.Read(buffer)
				text.Write(buffer[:n])
				if row["mode"] == "cancel" {
					_ = response.Body.Close()
					break
				}
				if e == io.EOF {
					break
				}
				if e != nil {
					failure = e
					break
				}
			}
			_ = response.Body.Close()
			if text.String() != expected[i].Text {
				t.Errorf("Source prefix%q Go%q", expected[i].Text, text.String())
			}
			if expected[i].Error != nil {
				if failure == nil || failure.Error() != expected[i].Error.Message {
					t.Errorf("Source error%+v Go%v", expected[i].Error, failure)
				}
			} else if failure != nil {
				t.Fatal(failure)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("actual upstream handler escaped terminal/cancel")
			}
		})
	}
}

func TestStandaloneSingleLoopbackPortAndCloseJoinsActiveRequest(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "prefix")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	config := providerproxy.ProxyConfig{ID: "fixture", APIFormat: "anthropic", BaseURL: peer.URL, AuthStrategy: "api_key", SupportsNestedToolResultMedia: false}
	resource, e := NewStandalone(Options{Provider: func(context.Context, string) (*providerproxy.ProxyConfig, error) { return &config, nil }, Settings: func(context.Context) (protocol.JSONObject, error) {
		return object(json.RawMessage(`{"network":{"proxy":{"mode":"direct"}}}`)), nil
	}, Environment: func() map[string]string { return map[string]string{} }})
	if e != nil {
		t.Fatal(e)
	}
	port, e := resource.EnsurePort()
	if e != nil {
		t.Fatal(e)
	}
	second, e := resource.EnsurePort()
	if e != nil || second != port {
		t.Fatal("CLI proxy did not reuse original process port", port, second, e)
	}
	client := &http.Client{Timeout: time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	response, e := client.Get(url + "/health")
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(data) != `{"status":"ok"}` {
		t.Fatal(string(data))
	}
	response, e = client.Post(url+"/proxy/v1/messages", "application/json", strings.NewReader(`{"model":"fixture","stream":true,"messages":[]}`))
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if e = resource.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("closed listener still owns active upstream request")
	}
	if _, e = client.Get(url + "/health"); e == nil {
		t.Fatal("CLI close left old proxy listener")
	}
	if _, e = resource.EnsurePort(); e == nil {
		t.Fatal("closed CLI resource resurrected")
	}
}
