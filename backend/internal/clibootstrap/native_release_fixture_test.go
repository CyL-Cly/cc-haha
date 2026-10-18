package clibootstrap

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// These interactive consumers are not release-download tests. A real warm
// cache and the exact current version select Source's existing no-refresh
// branch, without changing privacy, auth, metadata or availability flags.
func seedNativeReleaseWarmFixture(t *testing.T, f nativePluginFixture, version string) {
	t.Helper()
	path := filepath.Join(f.config, ".claude.json")
	value := protocol.NewJSONObject()
	data, err := os.ReadFile(path)
	if err == nil {
		if err = value.UnmarshalJSON(data); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	value.Set("lastReleaseNotesSeen", quoteMain(version))
	data, err = value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, path, string(data))
	// A nonempty cached changelog with no bullet entries leaves the original
	// release/recent-activity panel absent while preserving a populated memo.
	f.write(t, filepath.Join(f.config, "cache", "changelog.md"), "## 0.0.0\n")
	guardNativeInteractiveReleaseFixture(t, f)
}

// The fixture guard preserves an existing private TLS proxy and otherwise
// connects only to literal loopback peers. It rejects a release download (or
// any other public target) before physical egress, and checks zero attempts at
// cleanup. The actual startup and cached-read producers remain unchanged.
func guardNativeInteractiveReleaseFixture(t *testing.T, f nativePluginFixture) {
	t.Helper()
	previous := f.n.Environment["HTTPS_PROXY"]
	var privateProxy string
	if previous != "" {
		parsed, err := url.Parse(previous)
		if err != nil || parsed.Scheme != "http" || net.ParseIP(parsed.Hostname()) == nil || !net.ParseIP(parsed.Hostname()).IsLoopback() || parsed.Port() == "" {
			t.Fatal("release fixture requires an existing explicit loopback proxy", previous, err)
		}
		privateProxy = parsed.Host
	}
	var mu sync.Mutex
	connections := []net.Conn{}
	var external atomic.Int32
	transport := &http.Transport{Proxy: nil}
	guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			ip := net.ParseIP(r.URL.Hostname())
			if r.URL.Scheme != "http" || ip == nil || !ip.IsLoopback() {
				external.Add(1)
				t.Error("interactive startup used unowned proxy request", r.Method, r.URL)
				w.WriteHeader(http.StatusForbidden)
				return
			}
			request := r.Clone(r.Context())
			request.RequestURI = ""
			response, err := transport.RoundTrip(request)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			defer response.Body.Close()
			for key, values := range response.Header {
				w.Header()[key] = values
			}
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(nativeReleaseFixtureStreamingWriter{w}, response.Body)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		ip := net.ParseIP(host)
		if err != nil || strings.EqualFold(host, "raw.githubusercontent.com") || privateProxy == "" && (ip == nil || !ip.IsLoopback()) {
			external.Add(1)
			t.Error("interactive startup attempted unowned external CONNECT", r.Host, err)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		target := r.Host
		if privateProxy != "" {
			target = privateProxy
		}
		upstream, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", target)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		mu.Lock()
		connections = append(connections, upstream, client)
		mu.Unlock()
		if privateProxy != "" {
			if err = r.Write(upstream); err != nil {
				return
			}
		} else {
			if _, err = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
				return
			}
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffer); close(done); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	}))
	f.n.Environment["HTTPS_PROXY"] = guard.URL
	t.Cleanup(func() {
		mu.Lock()
		for _, connection := range connections {
			_ = connection.Close()
		}
		mu.Unlock()
		guard.Close()
		transport.CloseIdleConnections()
		if external.Load() != 0 {
			t.Fatal("interactive warm fixture attempted public HTTP", external.Load())
		}
		t.Log("actual interactive startup private CONNECT guard: zero external attempts")
	})
}

type nativeReleaseFixtureStreamingWriter struct{ http.ResponseWriter }

func (w nativeReleaseFixtureStreamingWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if flush, ok := w.ResponseWriter.(http.Flusher); ok {
		flush.Flush()
	}
	return n, err
}

func TestNativeInteractiveWarmReleaseFixtureRetainsActualStartupAndRejectsExternalHTTP(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.write(t, filepath.Join(f.config, ".claude.json"), `{"future":{"keep":17}}`)
	seedNativeReleaseWarmFixture(t, f, "actual-private-current-version")
	var requests atomic.Int32
	guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("warm ordinary startup attempted external HTTP", r.Method, r.Host, r.URL)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer guard.Close()
	f.n.Environment["HTTPS_PROXY"] = guard.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-private-warm-release"
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, Interactive: true, Version: "actual-private-current-version", Platform: "darwin", PID: os.Getpid(), Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, SystemPrompt: "Private startup without inference", HasCustomSystemPrompt: true, Tools: []string{""}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.terminalExportStartup == nil || m.nativeLocalCommands == nil || m.nativeLocalCommands.releaseNotes.Memory() != "## 0.0.0\n" || requests.Load() != 0 {
		t.Fatal("actual ordinary cache producer or no-refresh branch missing", requests.Load())
	}
	if err = m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("release HTTP escaped startup lifetime", requests.Load())
	}
	global, err := m.Global.Get()
	if err != nil {
		t.Fatal(err)
	}
	future, _ := global.Object("future")
	if global.String("lastReleaseNotesSeen") != "actual-private-current-version" || string(future.Get("keep")) != `17` {
		t.Fatal("warm fixture changed unknown global fields", err, global)
	}
}
