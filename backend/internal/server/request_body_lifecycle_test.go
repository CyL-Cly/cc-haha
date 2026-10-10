package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/conversationapi"
)

func TestActualSourceDelayedConversationBodyAfterPhysicalDisconnect(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source requires pinned Bun")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/request-abort-body.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REQUEST_ABORT_BODY_FIXTURE=1", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]string{"home": home})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Mode                            string
			ResponseStatus, OperationStatus int
			State                           string
			PendingAtResponse               bool
		}
		Capabilities         struct{ Blocked []string }
		EarlyResponsePooling []struct {
			Mode         string
			Calls        int
			Statuses     []int
			FirstHeaders string
		}
		SizeBoundaries []struct {
			Declared  int64
			Delivered bool
			Status    int
			Body      string
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != 2 || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("Source %v %s", e, out)
	}
	if len(source.SizeBoundaries) != 2 || source.SizeBoundaries[0].Declared != apiBodyLimit || !source.SizeBoundaries[0].Delivered || source.SizeBoundaries[0].Status != 204 || source.SizeBoundaries[1].Declared != apiBodyLimit+1 || source.SizeBoundaries[1].Delivered || source.SizeBoundaries[1].Status != 413 {
		t.Fatalf("original main listener body bound %+v", source.SizeBoundaries)
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/conversations/existing", nil)
	req.ContentLength = source.SizeBoundaries[1].Declared
	w := httptest.NewRecorder()
	s := lifecycleServer(t)
	s.serveAPILifecycle(w, req, true, func(http.ResponseWriter, *http.Request) { t.Error("body above original limit reached producer") })
	if w.Code != source.SizeBoundaries[1].Status || w.Body.String() != source.SizeBoundaries[1].Body {
		t.Fatal(w.Code)
	}
	if len(source.EarlyResponsePooling) != 2 {
		t.Fatalf("original unread-body pooling missing %+v", source.EarlyResponsePooling)
	}
	for _, row := range source.EarlyResponsePooling {
		if row.Calls != 2 || len(row.Statuses) != 2 || row.Statuses[0] != 405 || row.Statuses[1] != 200 || strings.Contains(strings.ToLower(row.FirstHeaders), "connection: close") {
			t.Fatalf("original unread-body pooling %+v", row)
		}
		t.Run("pooling-"+row.Mode, func(t *testing.T) {
			actual := lifecycleServer(t)
			peer := httptest.NewServer(actual)
			defer peer.Close()
			conn, e := net.Dial("tcp", peer.Listener.Addr().String())
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			reader := bufio.NewReader(conn)
			first, tail := "abc", ""
			if row.Mode == "partial" {
				first, tail = "a", "bc"
			}
			_, e = fmt.Fprintf(conn, "GET /api/conversations/pooling HTTP/1.1\r\nHost: %s\r\nContent-Length: 3\r\n\r\n%s", peer.Listener.Addr(), first)
			if e != nil {
				t.Fatal(e)
			}
			res, e := http.ReadResponse(reader, nil)
			if e != nil {
				t.Fatal(e)
			}
			_, e = io.ReadAll(res.Body)
			res.Body.Close()
			if e != nil || res.StatusCode != row.Statuses[0] || res.Close {
				t.Fatal("unread request changed original first response", res.StatusCode, res.Close, e)
			}
			_, e = fmt.Fprintf(conn, "%sGET /api/conversations/pooling/status HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", tail, peer.Listener.Addr())
			if e != nil {
				t.Fatal(e)
			}
			res, e = http.ReadResponse(reader, nil)
			if e != nil {
				t.Fatal("original connection not reusable", e)
			}
			_, e = io.ReadAll(res.Body)
			res.Body.Close()
			if e != nil || res.StatusCode != row.Statuses[1] {
				t.Fatal("pooled operation", res.StatusCode, e)
			}
		})
	}
	for _, row := range source.Rows {
		t.Run(row.Mode, func(t *testing.T) {
			if row.ResponseStatus != 499 || !row.PendingAtResponse {
				t.Fatalf("original lifecycle %+v", row)
			}
			s := lifecycleServer(t)
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan int, 1)
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			s.conversations, e = conversationapi.New(conversationapi.Options{Exists: func(context.Context, string) (bool, error) { close(entered); <-release; return true, nil }, Activity: s.conversationActivity, UUID: uuid})
			if e != nil {
				t.Fatal(e)
			}
			returned := make(chan int, 1)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture := &lifecycleStatusWriter{ResponseWriter: w}
				s.serveAPILifecycle(capture, r, true, func(w http.ResponseWriter, r *http.Request) {
					capture := &lifecycleStatusWriter{ResponseWriter: w}
					if e := s.conversationsHTTP(capture, r, []string{"api", "conversations", "existing"}); e != nil {
						writeError(capture, e)
					}
					finished <- capture.status
				})
				returned <- capture.status
			}))
			defer peer.Close()
			conn, e := net.Dial("tcp", peer.Listener.Addr().String())
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			body := []byte(`{"content":"complete request survives delayed Exists"}`)
			sent := body
			if row.Mode == "partial" {
				sent = body[:len(body)-7]
			}
			_, e = fmt.Fprintf(conn, "POST /api/conversations/existing HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n%s", peer.Listener.Addr(), len(body), sent)
			if e != nil {
				t.Fatal(e)
			}
			lifecycleReceive(t, entered)
			conn.Close()
			select {
			case status := <-returned:
				if status != row.ResponseStatus {
					t.Errorf("Go response%d Source%d", status, row.ResponseStatus)
				}
			case <-time.After(time.Second):
				t.Error("Go did not detect physical disconnect while Exists held")
			}
			unblock()
			if status := lifecycleReceive(t, finished); status != row.OperationStatus {
				t.Errorf("Go operation%d Source%d", status, row.OperationStatus)
			}
			status, e := s.conversations.Status(context.Background(), "existing")
			if e != nil {
				t.Fatal(e)
			}
			if status.String("state") != row.State {
				t.Errorf("Go actual state%s Source%s", status.String("state"), row.State)
			}
		})
	}
}

func TestServerCloseDeadlineLeavesSingleFinalizerOwningActiveOperation(t *testing.T) {
	s := lifecycleServer(t)
	entered, canceled, release, finished, shutdown := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var shutdownCalls atomic.Int32
	s.options.OnShutdown = func(ctx context.Context) error {
		shutdownCalls.Add(1)
		close(shutdown)
		select {
		case <-finished:
		default:
			t.Error("native shutdown before actual API join")
		}
		if ctx.Err() != nil {
			t.Error("caller deadline leaked into finalizer cleanup")
		}
		return nil
	}
	returned := make(chan struct{})
	go func() {
		s.serveAPILifecycle(httptest.NewRecorder(), httptest.NewRequest("GET", "http://127.0.0.1/api/pending", nil), true, func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-r.Context().Done()
			close(canceled)
			<-release
			close(finished)
		})
		close(returned)
	}()
	lifecycleReceive(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	e := s.Close(ctx)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("caller wait %v", e)
	}
	lifecycleReceive(t, canceled)
	select {
	case <-shutdown:
		t.Error("deadline triggered native shutdown before join")
	default:
	}
	select {
	case <-s.closeDone:
		t.Error("deadline marked actual finalization complete")
	default:
	}
	s.serveAPILifecycle(httptest.NewRecorder(), httptest.NewRequest("GET", "http://127.0.0.1/api/new", nil), true, func(http.ResponseWriter, *http.Request) { t.Error("new operation admitted during close") })
	unblock()
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	lifecycleReceive(t, returned)
	lifecycleReceive(t, shutdown)
	if shutdownCalls.Load() != 1 {
		t.Fatal("duplicate finalizers", shutdownCalls.Load())
	}
}

func TestPhysicalOwnedRequestBodySpillsWithExactBytesAndReleasesAfterDetachedConsumer(t *testing.T) {
	s := lifecycleServer(t)
	payload := bytes.Repeat([]byte("voice-and-json-exact-\x00\xff"), 180000)
	started, release, finished, returned := make(chan *apiRequestBody, 1), make(chan struct{}), make(chan error, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveAPILifecycle(w, r, true, func(w http.ResponseWriter, r *http.Request) {
			body, ok := r.Body.(*apiRequestBody)
			if !ok {
				finished <- errors.New("missing owned Body")
				return
			}
			started <- body
			<-release
			actual, e := io.ReadAll(r.Body)
			if e == nil && !bytes.Equal(actual, payload) {
				e = errors.New("owned bytes changed")
			}
			finished <- e
		})
		close(returned)
	}))
	defer peer.Close()
	clientCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(clientCtx, "POST", peer.URL, bytes.NewReader(payload))
	client := make(chan error, 1)
	go func() {
		res, e := peer.Client().Do(req)
		if res != nil {
			res.Body.Close()
		}
		client <- e
	}()
	body := lifecycleReceive(t, started)
	lifecycleReceive(t, body.done)
	body.mu.Lock()
	file := body.file
	memorySize := len(body.memory)
	body.mu.Unlock()
	if file == nil || memorySize > apiBodyMemoryLimit {
		t.Fatal("upload not bounded", memorySize)
	}
	info, e := file.Stat()
	if e != nil || info.Mode().Perm() != 0600 || info.Size() != int64(len(payload)) {
		t.Fatal("private spool", info, e)
	}
	name := file.Name()
	cancel()
	if e = lifecycleReceive(t, client); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	lifecycleReceive(t, returned)
	if _, e = os.Stat(name); e != nil {
		t.Fatal("detached consumer lost owned bytes", e)
	}
	unblock()
	if e = lifecycleReceive(t, finished); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(name); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("spool escaped actual join", e)
	}
}

func TestPhysicalPartialUploadServerCloseInterruptsAndJoinsIngress(t *testing.T) {
	s := lifecycleServer(t)
	started, consumerDone, returned := make(chan *apiRequestBody, 1), make(chan error, 1), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveAPILifecycle(w, r, true, func(w http.ResponseWriter, r *http.Request) {
			started <- r.Body.(*apiRequestBody)
			_, e := io.ReadAll(r.Body)
			consumerDone <- e
		})
		close(returned)
	}))
	defer peer.Close()
	conn, e := net.Dial("tcp", peer.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_, e = fmt.Fprintf(conn, "POST /api/pending HTTP/1.1\r\nHost: %s\r\nContent-Length: 1000000\r\n\r\npartial", peer.Listener.Addr())
	if e != nil {
		t.Fatal(e)
	}
	body := lifecycleReceive(t, started)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = s.Close(ctx); e != nil {
		t.Fatal("partial upload prevented close join", e)
	}
	if e = lifecycleReceive(t, consumerDone); e == nil {
		t.Fatal("partial body became fabricated successful EOF")
	}
	lifecycleReceive(t, body.done)
	lifecycleReceive(t, returned)
}

func TestPhysicalAPIConsumerCanRejectAfterPrefixWithoutWaitingForRestOfUpload(t *testing.T) {
	s := lifecycleServer(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveAPILifecycle(w, r, true, func(w http.ResponseWriter, r *http.Request) {
			var prefix [3]byte
			if _, e := io.ReadFull(r.Body, prefix[:]); e != nil || string(prefix[:]) != "abc" {
				t.Error("streaming prefix changed", e)
			}
			writeJSON(w, 400, map[string]string{"error": "prefix rejected"})
		})
	}))
	defer peer.Close()
	conn, e := net.Dial("tcp", peer.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, e = fmt.Fprintf(conn, "POST /api/upload HTTP/1.1\r\nHost: %s\r\nContent-Length: 1000000\r\n\r\nabc", peer.Listener.Addr())
	if e != nil {
		t.Fatal(e)
	}
	var head [32]byte
	n, e := conn.Read(head[:])
	if e != nil || !bytes.Contains(head[:n], []byte("400")) {
		t.Fatalf("early response %q %v", head[:n], e)
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestActualServerRouterDetachedConversationBodyAndUnknownPayloadPreserved(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			s := lifecycleServer(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			service, e := conversationapi.New(conversationapi.Options{Exists: func(context.Context, string) (bool, error) { close(entered); <-release; return true, nil }, Activity: s.conversationActivity, UUID: uuid})
			if e != nil {
				t.Fatal(e)
			}
			s.conversations = service
			returned := make(chan int, 1)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture := &lifecycleStatusWriter{ResponseWriter: w}
				s.ServeHTTP(capture, r)
				returned <- capture.status
			}))
			defer peer.Close()
			conn, e := net.Dial("tcp", peer.Listener.Addr().String())
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			body := []byte(`{"content":"完整 body keeps actual legacy queued side effect","future":{"preserved":true}}`)
			sent := body
			if !complete {
				sent = body[:len(body)-7]
			}
			_, e = fmt.Fprintf(conn, "POST /api/conversations/existing HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", peer.Listener.Addr(), len(body), sent)
			if e != nil {
				t.Fatal(e)
			}
			lifecycleReceive(t, entered)
			conn.Close()
			if status := lifecycleReceive(t, returned); status != 499 {
				t.Fatal(status)
			}
			unblock()
			joined := make(chan struct{})
			go func() { s.waitAPIOperations(); close(joined) }()
			lifecycleReceive(t, joined)
			status, e := s.conversations.Status(context.Background(), "existing")
			if e != nil {
				t.Fatal(e)
			}
			want := "idle"
			if complete {
				want = "thinking"
			}
			if status.String("state") != want {
				t.Fatalf("actual routed business %s want%s", status.String("state"), want)
			}
		})
	}
}

func TestPhysicalOwnedBodyKeepsHTTPConnectionReusableAndDoesNotCaptureNonRacingBody(t *testing.T) {
	s := lifecycleServer(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveAPILifecycle(w, r, true, func(w http.ResponseWriter, r *http.Request) {
			data, e := io.ReadAll(r.Body)
			if e != nil {
				t.Error(e)
			}
			writeJSON(w, 200, map[string]string{"body": string(data)})
		})
	}))
	defer peer.Close()
	conn, e := net.Dial("tcp", peer.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(conn)
	for _, data := range []string{"initial", "next"} {
		_, e = fmt.Fprintf(conn, "POST /api/input HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\n%s", peer.Listener.Addr(), len(data), data)
		if e != nil {
			t.Fatal(e)
		}
		res, e := http.ReadResponse(reader, nil)
		if e != nil {
			t.Fatal("pooled connection changed", e)
		}
		raw, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil || res.StatusCode != 200 || !bytes.Contains(raw, []byte(data)) {
			t.Fatal(res.StatusCode, string(raw), e)
		}
	}
	original := io.NopCloser(bytes.NewBufferString("ordinary direct body"))
	req := httptest.NewRequest("POST", "http://127.0.0.1/sdk/session-collaboration/create", nil)
	req.Body = original
	s.serveAPILifecycle(httptest.NewRecorder(), req, false, func(_ http.ResponseWriter, r *http.Request) {
		if r.Body != original {
			t.Error("non-racing Source caller unnecessarily captured body")
		}
	})
}

// Unknown-length uploads use the same actual native ingress/spill path. The
// limit value is established above by pinned Source HTTP, not a test override.
func TestOwnedUnknownLengthUploadSizeResponsePrecedesConsumerReadError(t *testing.T) {
	s := lifecycleServer(t)
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/input", nil)
	req.ContentLength = -1
	req.Body = io.NopCloser(io.LimitReader(lifecycleZeroReader{}, apiBodyLimit+1))
	w := httptest.NewRecorder()
	var body *apiRequestBody
	s.serveAPILifecycle(w, req, true, func(w http.ResponseWriter, r *http.Request) {
		body = r.Body.(*apiRequestBody)
		_, e := io.Copy(io.Discard, r.Body)
		if !errors.Is(e, errAPIBodyLimit) {
			t.Error("unknown-length upload limit lost", e)
		}
		writeJSON(w, 400, map[string]string{"error": "consumer invalid input"})
	})
	if w.Code != 413 || w.Body.Len() != 0 {
		t.Fatal("consumer response displaced native input limit", w.Code)
	}
	if e := s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	body.mu.Lock()
	file := body.file
	size := body.size
	memorySize := len(body.memory)
	body.mu.Unlock()
	if size > apiBodyLimit || memorySize != 0 || file == nil {
		t.Fatal("unknown-length spool not bounded", size, memorySize)
	}
	if _, e := os.Stat(file.Name()); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("oversize spool escaped actual cleanup", e)
	}
}

type lifecycleZeroReader struct{}

func (lifecycleZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
