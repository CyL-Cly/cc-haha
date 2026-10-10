package imregistration

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestQQActualVendorRejectsStatusBeforeBodyFinishes(t *testing.T) {
	cert, key := qqCertificate(t)
	var expected []map[string]any
	runQQSource(t, "native-qq-http-lifecycle.fixture.ts", "CC_HAHA_QQ_HTTP_LIFECYCLE_FIXTURE", map[string]any{"cert": cert, "key": key, "modes": []string{"reject-body"}}, &expected)
	started, release := make(chan struct{}), make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(503)
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	defer close(release)
	s, e := NewQQ(QQOptions{Client: qqFixtureClient(peer)})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	done := make(chan error, 1)
	go func() { _, e := s.Start(context.Background(), record()); done <- e }()
	<-started
	select {
	case e := <-done:
		if e == nil || e.Error() != "获取绑定任务失败: "+expected[0]["error"].(string) {
			t.Fatalf("Source non200 headers result: %v vs %v", e, expected)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Source rejects non200 headers immediately; Go incorrectly awaits its response body")
	}
}

type qqPlaintextObserver struct {
	net.Conn
	observed chan struct{}
	once     sync.Once
}

func (c *qqPlaintextObserver) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	if bytes.Contains(p[:n], append([]byte(`{"retcode":1,"msg":"`), 0xe4)) {
		c.once.Do(func() { close(c.observed) })
	}
	return n, e
}

func TestQQActualVendorDecodesEveryPhysicalBufferChunk(t *testing.T) {
	cert, key := qqCertificate(t)
	var expected []map[string]any
	runQQSource(t, "native-qq-http-lifecycle.fixture.ts", "CC_HAHA_QQ_HTTP_LIFECYCLE_FIXTURE", map[string]any{"cert": cert, "key": key, "modes": []string{"split-utf8"}}, &expected)
	first := make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write(append([]byte(`{"retcode":1,"msg":"`), 0xe4))
		w.(http.Flusher).Flush()
		select {
		case <-first:
		case <-r.Context().Done():
			return
		}
		w.Write(append([]byte{0xb8, 0xad}, []byte(`"}`)...))
	}))
	defer peer.Close()
	address := strings.TrimPrefix(peer.URL, "https://")
	client := qqFixtureClient(peer)
	tr := client.Transport.(*http.Transport)
	tr.DialTLSContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != "q.qq.com:443" {
			return nil, fmt.Errorf("unowned QQ TLS target")
		}
		raw, e := (&net.Dialer{}).DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		conn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true})
		if e = conn.HandshakeContext(ctx); e != nil {
			raw.Close()
			return nil, e
		}
		return &qqPlaintextObserver{Conn: conn, observed: first}, nil
	}
	s, e := NewQQ(QQOptions{Client: client})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	_, e = s.Start(context.Background(), record())
	if e == nil || e.Error() != "获取绑定任务失败: "+expected[0]["error"].(string) {
		t.Fatalf("Source per-buffer UTF8 decoding mismatch: Go%v Source%v", e, expected[0])
	}
}

func TestQQActualVendorAndGoSocketInactivityTimeout(t *testing.T) {
	cert, key := qqCertificate(t)
	var expected []map[string]any
	runQQSource(t, "native-qq-http-lifecycle.fixture.ts", "CC_HAHA_QQ_HTTP_LIFECYCLE_FIXTURE", map[string]any{"cert": cert, "key": key, "modes": []string{"no-headers", "partial-body"}}, &expected)
	t.Logf("Actual original socket observations: %v", expected)
	for i, mode := range []string{"no-headers", "partial-body"} {
		i, mode := i, mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			closed := make(chan struct{})
			release := make(chan struct{})
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if mode == "partial-body" {
					io.WriteString(w, `{"retcode":`)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
					close(closed)
				case <-release:
				}
			}))
			defer peer.Close()
			defer close(release)
			s, e := NewQQ(QQOptions{Client: qqFixtureClient(peer)})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			before := time.Now()
			done := make(chan error, 1)
			go func() { _, err := s.Start(context.Background(), record()); done <- err }()
			if expected[i]["pending"] == true {
				select {
				case err := <-done:
					t.Fatalf("Source partial body pending, Go settled: %v", err)
				case <-time.After(12 * time.Second):
				}
				if e = s.Close(context.Background()); e != nil {
					t.Fatal(e)
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("pending body unexpectedly succeeded")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("pending body not joined by Close")
				}
				return
			}
			e = <-done
			elapsed := time.Since(before)

			if e == nil || e.Error() != "获取绑定任务失败: "+expected[i]["error"].(string) {
				t.Fatalf("actual Source timeout mismatch: %v vs %v", e, expected[i])
			}
			if elapsed < 9*time.Second || elapsed > 14*time.Second {
				t.Fatalf("vendor10s socket inactivity, actual %v", elapsed)
			}
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("timed out physical socket not closed")
			}
		})
	}
}

func TestQQCloseCancelsActualTLSAndJoinsWorkers(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			started, closed := make(chan struct{}), make(chan struct{})
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if partial {
					io.WriteString(w, `{"retcode":`)
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
				close(closed)
			}))
			defer peer.Close()
			s, e := NewQQ(QQOptions{Client: qqFixtureClient(peer)})
			if e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() { _, e := s.Start(context.Background(), record()); done <- e }()
			<-started
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if e = s.Close(ctx); e != nil {
				t.Fatal(e)
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("owned TLS socket not cancelled")
			}
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("closed connector start succeeded")
				}
			case <-ctx.Done():
				t.Fatal("Close did not join actual worker")
			}
			if _, e = s.Start(context.Background(), record()); e == nil || e.Error() != "QQ registration is closed" {
				t.Fatalf("closed service restarted: %v", e)
			}
			if e = s.Close(ctx); e != nil {
				t.Fatal("idempotent Close", e)
			}
		})
	}
}

func TestQQReusedSocketArmsOriginalRequestTimeout(t *testing.T) {
	cert, key := qqCertificate(t)
	var expected []map[string]any
	runQQSource(t, "native-qq-http-lifecycle.fixture.ts", "CC_HAHA_QQ_HTTP_LIFECYCLE_FIXTURE", map[string]any{"cert": cert, "key": key, "modes": []string{"reuse-no-headers"}}, &expected)
	var mu sync.Mutex
	requests := 0
	connections := map[string]bool{}
	release := make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		requests++
		at := requests
		connections[r.RemoteAddr] = true
		mu.Unlock()
		if at == 1 {
			io.WriteString(w, `{"retcode":0,"data":{"task_id":"private-task"}}`)
			return
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer peer.Close()
	defer close(release)
	s, e := NewQQ(QQOptions{Client: qqFixtureClient(peer)})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	if _, e = s.http.post(s.ioContext, QQCreateBindURL, record(field("key", "private-1"))); e != nil {
		t.Fatal(e)
	}
	before := time.Now()
	_, e = s.http.post(s.ioContext, QQCreateBindURL, record(field("key", "private-2")))
	if e == nil || e.Error() != expected[0]["error"].(string) {
		t.Fatalf("reused original timer: %v vs%v", e, expected)
	}
	if elapsed := time.Since(before); elapsed < 9*time.Second || elapsed > 14*time.Second {
		t.Fatalf("reused timer not10s: %v", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 || len(connections) != 1 {
		t.Fatalf("test did not exercise actual socket reuse: requests%d sockets%d", requests, len(connections))
	}
}

func TestQQStartCancellationPreservesVendorInflightHTTPThenClose(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	closed := make(chan struct{})
	var mu sync.Mutex
	closedEarly := false
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			mu.Lock()
			closedEarly = true
			mu.Unlock()
			close(closed)
		case <-release:
			io.WriteString(w, `{"retcode":0,"data":{"task_id":"private-late-task"}}`)
		}
	}))
	defer peer.Close()
	defer close(release)
	s, e := NewQQ(QQOptions{Client: qqFixtureClient(peer)})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() { _, e := s.Start(ctx, record()); done <- e }()
	<-started
	cause := errors.New("private caller disconnected")
	cancel(cause)
	if e = <-done; !errors.Is(e, cause) {
		t.Fatalf("caller cause lost: %v", e)
	}
	mu.Lock()
	if closedEarly {
		t.Error("vendor dispose incorrectly canceled active HTTP")
	}
	mu.Unlock()
	if v, e := s.PollJSON(context.Background(), encoded("private-fixed-key")); e != nil || !strings.Contains(string(v.Get("status")), "not_started") {
		t.Fatalf("cancelled start became visible: %v %v", v, e)
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not physically close TLS peer")
	}
	mu.Lock()
	if !closedEarly {
		t.Error("administrative Close did not cancel physical HTTP")
	}
	mu.Unlock()
}
