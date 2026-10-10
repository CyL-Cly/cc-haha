package httphook

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"encoding/pem"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/andybalholm/brotli"
)

func httpObject(t *testing.T, text string) protocol.JSONObject {
	t.Helper()
	var value protocol.JSONObject
	if err := value.UnmarshalJSON([]byte(text)); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestActualSourceHTTPPhysicalPostCompressionNoRedirectHeadersTLSAndCancellation(t *testing.T) {
	type observation struct{ Path, Body, Method, Token, Hidden, Latin, ContentType, Accept string }
	var mu sync.Mutex
	observations := []observation{}
	var redirected atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		observations = append(observations, observation{r.URL.Path, string(body), r.Method, r.Header.Get("X-Token"), r.Header.Get("X-Hidden"), r.Header.Get("X-Latin"), r.Header.Get("Content-Type"), r.Header.Get("Accept")})
		mu.Unlock()
		if r.URL.Path == "/wait" {
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/redirected" {
			redirected.Add(1)
		}
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/redirected")
			w.WriteHeader(302)
			_, _ = w.Write([]byte("redirect body"))
			return
		}
		if r.URL.Path == "/denied" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte("denied body"))
			return
		}
		if r.URL.Path == "/empty" {
			w.WriteHeader(204)
			return
		}
		plain := []byte("\xef\xbb\xbf{\"actual\":\"transport ✓\",\"invalid\":\"\xff\"}")
		var target io.Writer = w
		var compressor io.WriteCloser
		switch r.URL.Path {
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			compressor = gzip.NewWriter(w)
		case "/deflate":
			w.Header().Set("Content-Encoding", "deflate")
			compressor = zlib.NewWriter(w)
		case "/raw-deflate":
			w.Header().Set("Content-Encoding", "deflate")
			compressor, _ = flate.NewWriter(w, flate.DefaultCompression)
		case "/br":
			w.Header().Set("Content-Encoding", "br")
			compressor = brotli.NewWriter(w)
		}
		if compressor != nil {
			target = compressor
			defer compressor.Close()
		}
		_, _ = target.Write(plain)
	})
	peer := httptest.NewServer(handler)
	defer peer.Close()
	tlsPeer := httptest.NewTLSServer(handler)
	defer tlsPeer.Close()
	type row struct {
		Hook        protocol.JSONObject `json:"hook"`
		Settings    protocol.JSONObject `json:"settings"`
		Environment map[string]string   `json:"environment"`
		Body        string              `json:"body"`
		PreAborted  bool                `json:"preAborted"`
	}
	rows := []row{}
	native := []Result{}
	for _, path := range []string{"/plain", "/gzip", "/deflate", "/raw-deflate", "/br", "/redirect", "/denied", "/empty", "/wait", "/pre-aborted", "/tls"} {
		endpoint := peer.URL + path
		options := NativeOptions{}
		if path == "/tls" {
			endpoint = tlsPeer.URL + path
			options.VerifiedTLS = tlsPeer.Client().Transport.(*http.Transport).TLSClientConfig
		}
		hook := httpObject(t, `{"type":"http","headers":{"X-Token":"$HTTP_HOOK_FIXTURE_TOKEN","X-Hidden":"$HTTP_HOOK_FIXTURE_HIDDEN","X-Latin":"é","content-type":"fixture/type"},"allowedEnvVars":["HTTP_HOOK_FIXTURE_TOKEN","HTTP_HOOK_FIXTURE_HIDDEN"]}`)
		hook.Set("url", quote(endpoint))
		if path == "/wait" {
			hook.Set("timeout", []byte(`0.05`))
		}
		settings := httpObject(t, `{"httpHookAllowedEnvVars":["HTTP_HOOK_FIXTURE_TOKEN"],"future":{"unknown":true}}`)
		env := map[string]string{"HTTP_HOOK_FIXTURE_TOKEN": "fake\r\n\x00token", "HTTP_HOOK_FIXTURE_HIDDEN": "must-not-leak"}
		options.Environment = env
		ctx, cancel := context.WithCancel(context.Background())
		if path == "/pre-aborted" {
			cancel()
		}
		body := `{"actual":"input","lone":"\ud800","unknown":{"kept":true}}`
		result, err := Execute(ctx, hook, settings, []byte(body), options)
		cancel()
		if err != nil {
			t.Fatal(path, err)
		}
		rows = append(rows, row{hook, settings, env, body, path == "/pre-aborted"})
		native = append(native, result)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-http-hook-transport.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HTTP_HOOK_TRANSPORT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsPeer.Certificate().Raw}))
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows, "ca": ca})
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct{ Rows []Result }
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(native) {
		t.Fatal(err, string(output))
	}
	for i := range native {
		if !reflect.DeepEqual(native[i], source.Rows[i]) {
			t.Errorf("row%d Go%+v Source%+v", i, native[i], source.Rows[i])
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect followed", redirected.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observations) != 20 {
		t.Fatalf("actual wire count=%d want20", len(observations))
	}
	for _, actual := range observations {
		if actual.Method != "POST" || actual.Token != "faketoken" || actual.Hidden != "" || actual.Latin != "é" || actual.ContentType != "fixture/type" || actual.Accept != "application/json, text/plain, */*" || !strings.Contains(actual.Body, `"lone":"\ud800"`) {
			t.Errorf("actual request %+v", actual)
		}
	}
}

func TestHTTPGuardValidatesEveryDNSAnswerAndUsesValidatedAddressWithoutRebinding(t *testing.T) {
	var resolved, dialed atomic.Int32
	hook := httpObject(t, `{"url":"http://fixture.invalid:123/hook"}`)
	options := NativeOptions{Resolve: func(context.Context, string) ([]net.IPAddr, error) {
		resolved.Add(1)
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("169.254.169.254")}}, nil
	}, Dial: func(context.Context, string, string) (net.Conn, error) {
		dialed.Add(1)
		t.Error("blocked DNS answer reached dial")
		return nil, io.EOF
	}}
	result, err := Execute(context.Background(), hook, protocol.NewJSONObject(), []byte(`{}`), options)
	if err != nil || result.OK || result.Error == nil || resolved.Load() != 1 || dialed.Load() != 0 {
		t.Fatal(result, err, resolved.Load(), dialed.Load())
	}
	if string(result.Error) != string(quote(BlockedAddressMessage("fixture.invalid", "169.254.169.254"))) {
		t.Fatal(string(result.Error))
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) }))
	defer peer.Close()
	endpoint := strings.TrimPrefix(peer.URL, "http://")
	options.Resolve = func(context.Context, string) ([]net.IPAddr, error) {
		resolved.Add(1)
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	options.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed.Add(1)
		if address != "127.0.0.1:123" {
			t.Fatal("DNS address replaced", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	}
	result, err = Execute(context.Background(), hook, protocol.NewJSONObject(), []byte(`{}`), options)
	if err != nil || !result.OK || resolved.Load() != 2 || dialed.Load() != 1 {
		t.Fatal(result, err, resolved.Load(), dialed.Load())
	}
}

func TestHTTPPhysicalCancellationJoinsActiveResponseAndVerifiedTLSIsRequired(t *testing.T) {
	started := make(chan struct{})
	ended := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(ended)
	}))
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	hook := httpObject(t, `{"url":`+string(quote(peer.URL))+`}`)
	done := make(chan Result, 1)
	go func() {
		out, err := Execute(ctx, hook, protocol.NewJSONObject(), []byte(`{}`), NativeOptions{})
		if err != nil {
			t.Error(err)
		}
		done <- out
	}()
	<-started
	cancel()
	select {
	case out := <-done:
		if !out.Aborted || string(out.Body) != `""` || out.StatusCode != nil {
			t.Fatal(out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP response cancellation did not join")
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("peer retained canceled connection")
	}
	tlsPeer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unverified TLS reached peer") }))
	defer tlsPeer.Close()
	hook.Set("url", quote(tlsPeer.URL))
	_, err := Execute(context.Background(), hook, protocol.NewJSONObject(), []byte(`{}`), NativeOptions{})
	if _, ok := err.(*CapabilityError); !ok {
		t.Fatal("missing verified TLS", err)
	}
}
