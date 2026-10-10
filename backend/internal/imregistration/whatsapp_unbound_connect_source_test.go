package imregistration

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
	"github.com/coder/websocket"
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
	"sync/atomic"
	"testing"
	"time"
)

type unboundConnectPeer struct {
	server                           *httptest.Server
	hello, closeSignal, closed       chan struct{}
	onceHello, onceClose, onceClosed sync.Once
	now                              *atomic.Int64
}

func newUnboundConnectPeer(t *testing.T, now *atomic.Int64) *unboundConnectPeer {
	t.Helper()
	p := &unboundConnectPeer{hello: make(chan struct{}), closeSignal: make(chan struct{}), closed: make(chan struct{}), now: now}
	p.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			io.WriteString(w, "a\nb\nc\nd\ne\nf\nconst version = [2, 3000, 1027934701];\n")
		case "/barrier":
			select {
			case <-p.hello:
				w.WriteHeader(200)
			case <-r.Context().Done():
			}
		case "/close":
			p.onceClose.Do(func() { close(p.closeSignal) })
			w.WriteHeader(200)
		case "/ws/chat":
			c, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if e != nil {
				t.Error(e)
				return
			}
			defer c.CloseNow()
			_, data, e := c.Read(r.Context())
			if e != nil {
				t.Error(e)
				return
			}
			if len(data) < 8 || string(data[:2]) != "WA" {
				t.Error("actual Noise client hello absent")
				return
			}
			if p.now != nil {
				p.now.Store(21001)
			}
			p.onceHello.Do(func() { close(p.hello) })
			select {
			case <-p.closeSignal:
			case <-r.Context().Done():
			}
			c.CloseNow()
			p.onceClosed.Do(func() { close(p.closed) })
		default:
			t.Errorf("unowned private path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(func() { p.onceClose.Do(func() { close(p.closeSignal) }); p.server.Close() })
	return p
}

type unboundConnectTransport struct{ peer *httptest.Server }

func (r unboundConnectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	target, _ := url.Parse(r.peer.URL)
	switch req.URL.String() {
	case wa.NodeWhatsAppVersionURL:
		u.Path = "/version"
	default:
		if req.URL.Host != "web.whatsapp.com" || req.URL.Path != "/ws/chat" {
			return nil, fmt.Errorf("unowned target")
		}
	}
	u.Host, u.Scheme = target.Host, target.Scheme
	copy.URL = &u
	return r.peer.Client().Transport.RoundTrip(copy)
}
func TestWhatsAppUnboundActualSourceSDKReturnsWaitingBeforeNoiseAndOwnsTerminalClose(t *testing.T) {
	home := t.TempDir()
	sourcePeer := newUnboundConnectPeer(t, nil)
	ca := filepath.Join(home, "ca.pem")
	cert := sourcePeer.server.Certificate()
	if _, e := x509.ParseCertificate(cert.Raw); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	inputFile := filepath.Join(home, "input.json")
	data, _ := json.Marshal(map[string]string{"home": home, "peer": sourcePeer.server.URL, "ca": ca})
	os.WriteFile(inputFile, data, 0600)
	fixture, _ := filepath.Abs("testdata/whatsapp-unbound-connect.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture, inputFile)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "NODE_EXTRA_CA_CERTS=" + ca, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source SDK %v %s", e, out)
	}
	// Bun emits known WebSocket compatibility warnings before the framed JSON.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var source struct{ Start, Waiting, Terminal, Expired protocol.JSONObject }
	if e = json.Unmarshal([]byte(lines[len(lines)-1]), &source); e != nil {
		t.Fatalf("Source result %v %s", e, out)
	}
	var now atomic.Int64
	now.Store(1000)
	goPeer := newUnboundConnectPeer(t, &now)
	s := NewWhatsAppLogin(WhatsAppLoginOptions{Now: func() time.Time { return time.UnixMilli(now.Load()) }, UUID: func() string { return source.Start.String("sessionKey") }, ClientOptions: wa.NodeWhatsAppOptions{HTTPTransport: unboundConnectTransport{goPeer.server}}})
	defer s.Close(context.Background())
	start, e := s.Start(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	waiting, e := s.Poll(start.String("sessionKey"))
	if e != nil {
		t.Fatal(e)
	}
	goPeer.onceClose.Do(func() { close(goPeer.closeSignal) })
	waWait(t, goPeer.closed)
	// Observe the actual connection-error event, without a sleep or extra socket.
	var terminal protocol.JSONObject
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		terminal, e = s.Poll(start.String("sessionKey"))
		if e != nil {
			t.Fatal(e)
		}
		if terminal.String("status") != "waiting" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("actual native connection did not publish terminal error")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	expired, e := s.Poll(start.String("sessionKey"))
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range []struct{ got, want protocol.JSONObject }{{start, source.Start}, {waiting, source.Waiting}, {terminal, source.Terminal}, {expired, source.Expired}} {
		a, _ := row.got.MarshalJSON()
		b, _ := row.want.MarshalJSON()
		var av, bv any
		json.Unmarshal(a, &av)
		json.Unmarshal(b, &bv)
		if !reflect.DeepEqual(av, bv) {
			t.Fatalf("original connecting/close mismatch Go%s Source%s", a, b)
		}
	}
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestOrdinaryWhatsAppConnectStillWaitsAndHeldNoisePeerCloseOrCancelDrains(t *testing.T) {
	for _, administrative := range []bool{false, true} {
		t.Run(fmt.Sprint(administrative), func(t *testing.T) {
			peer := newUnboundConnectPeer(t, nil)
			n, e := wa.NewNodeWhatsAppClient(context.Background(), wa.NodeWhatsAppOptions{AuthDir: t.TempDir(), DisableAutoReconnect: true, HTTPTransport: unboundConnectTransport{peer.server}})
			if e != nil {
				t.Fatal(e)
			}
			defer n.Close(context.Background())
			connect := make(chan error, 1)
			go func() { connect <- n.Connect(context.Background()) }()
			waWait(t, peer.hello)
			select {
			case e := <-connect:
				t.Fatalf("ordinary synchronous Connect returned before actual Noise completed: %v", e)
			default:
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if administrative {
				if e = n.Close(ctx); e != nil {
					t.Fatal("Close did not cancel and join held handshake", e)
				}
			} else {
				peer.onceClose.Do(func() { close(peer.closeSignal) })
			}
			select {
			case e := <-connect:
				if e == nil {
					t.Fatal("held incomplete Noise reported connected")
				}
			case <-ctx.Done():
				t.Fatal("physical peer/caller cancellation did not settle ordinary Connect")
			}
			if e = n.Close(ctx); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestWhatsAppQRAdministrativeCloseCancelsAndJoinsWithoutFailureCallback(t *testing.T) {
	peer := newUnboundConnectPeer(t, nil)
	var failures atomic.Int32
	n, e := wa.NewNodeWhatsAppClient(context.Background(), wa.NodeWhatsAppOptions{AuthDir: t.TempDir(), DisableAutoReconnect: true, HTTPTransport: unboundConnectTransport{peer.server}})
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close(context.Background())
	if e = n.StartLoginConnection(context.Background(), func(error) { failures.Add(1) }); e != nil {
		t.Fatal(e)
	}
	waWait(t, peer.hello)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = n.Close(ctx); e != nil {
		t.Fatal("owned async connection escaped administrative close", e)
	}
	if failures.Load() != 0 {
		t.Fatal("administrative cancellation emitted connection failure", failures.Load())
	}
	if e = n.Close(ctx); e != nil {
		t.Fatal("second close", e)
	}
}
