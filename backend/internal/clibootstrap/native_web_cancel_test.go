package clibootstrap

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func privateWebMain(t *testing.T, origin string, cert []byte) *MainPrint {
	t.Helper()
	n := nativeForTest(t)
	n.NativeProcess = true
	n.ManagedRoot = filepath.Join(n.Temp, "managed")
	_ = os.MkdirAll(n.ManagedRoot, 0700)
	n.Environment["PATH"] = "/usr/bin:/bin"
	n.Environment["ANTHROPIC_BASE_URL"] = origin
	n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "deepseek-flash"
	n.Environment["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0"
	n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private fixture secure absence")
	}
	n.ResolvePolicy = func() (*protocol.JSONObject, error) { o := protocol.NewJSONObject(); return &o, nil }
	if cert != nil {
		ca := filepath.Join(n.Temp, "ca.pem")
		_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0600)
		n.Environment["NODE_EXTRA_CA_CERTS"] = ca
	}
	_ = os.MkdirAll(n.Environment["CLAUDE_CONFIG_DIR"], 0700)
	_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"skipWebFetchPreflight":true}`), 0600)
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "deepseek-flash", SystemPrompt: "shared private web lifecycle fixture", Tools: []string{"WebFetch"}, AllowedTools: []string{"WebFetch"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 8, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	return m
}
func TestWebAuxiliaryCancellationJoinsRealStreamAndLeavesSessionUsable(t *testing.T) {
	var mu sync.Mutex
	posts := 0
	opened := make(chan struct{})
	closed := make(chan struct{})
	var origin string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/page" {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "real page")
			return
		}
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		posts++
		number := posts
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		switch number {
		case 1:
			fmt.Fprint(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(fmt.Sprintf(`{"type":"tool_use","id":"cancel-web","name":"WebFetch","input":{"url":%q,"prompt":"summary"}}`, origin+"/page"))}))
		case 2:
			fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"cancellable-aux\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-flash\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
			w.(http.Flusher).Flush()
			close(opened)
			<-r.Context().Done()
			close(closed)
		case 3:
			fmt.Fprint(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"still usable"}`)}))
		default:
			t.Errorf("unexpected request%d", number)
		}
	}))
	defer server.Close()
	origin = server.URL
	m := privateWebMain(t, origin, server.Certificate().Raw)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"start cancellable web request"}}`))
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("auxiliary HTTP stream never opened")
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("actual secondary HTTP body not closed")
	}
	select {
	case e := <-done:
		if e != nil && !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not settle original model/worker")
	}
	ctx, nextCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer nextCancel()
	if e := m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"continue after stop"}}`)); e != nil {
		t.Fatal(e)
	}
	if e := m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestTwoWebFetchAuxiliaryProducersUseOneOwnerAndActuallyOverlap(t *testing.T) {
	var mu sync.Mutex
	posts := 0
	aux := 0
	both := make(chan struct{})
	var origin string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/page") {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, r.URL.Path)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var request protocol.JSONObject
		_ = json.Unmarshal(body, &request)
		mu.Lock()
		posts++
		number := posts
		isAux := strings.Contains(string(request.Get("messages")), "Web page content:")
		if isAux {
			aux++
			if aux == 2 {
				close(both)
			}
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if isAux {
			select {
			case <-both:
			case <-time.After(3 * time.Second):
				t.Error("two Source concurrency-safe auxiliary requests were serialized")
			}
			fmt.Fprint(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"parallel summary"}`)}))
			return
		}
		if number == 1 {
			blocks := []protocol.JSONObject{}
			for i := 1; i <= 2; i++ {
				blocks = append(blocks, webObject(fmt.Sprintf(`{"type":"tool_use","id":"parallel-web-%d","name":"WebFetch","input":{"url":%q,"prompt":"summary"}}`, i, fmt.Sprintf("%s/page%d", origin, i))))
			}
			fmt.Fprint(w, webWire("deepseek-flash", blocks))
			return
		}
		fmt.Fprint(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"parallel complete"}`)}))
	}))
	defer server.Close()
	origin = server.URL
	m := privateWebMain(t, origin, server.Certificate().Raw)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"fetch two pages concurrently"}}`)); e != nil {
		t.Fatal(e)
	}
	if e := m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	count, secondaries := posts, aux
	mu.Unlock()
	if count != 4 || secondaries != 2 {
		t.Fatalf("shared actual requests=%d auxiliary=%d", count, secondaries)
	}
}
