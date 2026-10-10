package imruntime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSlackOriginalSocketModeActualTrustedWSSAckDisconnectReconnectAndClose(t *testing.T) {
	var mu sync.Mutex
	acks := []string{}
	closed := 0
	peerClosed := make(chan struct{}, 4)
	awaitClosed := func(phase string) {
		t.Helper()
		for i := 0; i < 2; i++ {
			select {
			case <-peerClosed:
			case <-time.After(time.Second):
				t.Fatalf("%s remote peer %d did not observe actual socket closure", phase, i+1)
			}
		}
	}
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer func() { conn.Close(); mu.Lock(); closed++; mu.Unlock(); peerClosed <- struct{}{} }()
		path := conn.Request().URL.Path
		first := strings.HasSuffix(path, "/one")
		_ = websocket.Message.Send(conn, "not-json")
		_ = websocket.JSON.Send(conn, map[string]string{"type": "hello", "envelope_id": "hello"})
		event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("events_api")}, protocol.JSONField{Name: "envelope_id", Value: raw("event-" + path[1:])}, protocol.JSONField{Name: "payload", Value: raw(map[string]string{"message": "private peer"})})
		_ = websocket.JSON.Send(conn, event)
		if first {
			_ = websocket.JSON.Send(conn, map[string]string{"type": "disconnect", "envelope_id": "disconnect", "reason": "refresh_requested"})
		}
		for {
			var ack protocol.JSONObject
			if websocket.JSON.Receive(conn, &ack) != nil {
				return
			}
			mu.Lock()
			acks = append(acks, ack.String("envelope_id"))
			mu.Unlock()
		}
	}}
	peer := httptest.NewTLSServer(ws)
	defer peer.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
	address := "wss" + strings.TrimPrefix(peer.URL, "https")
	home := t.TempDir()
	caPath := filepath.Join(home, "private-peer-ca.pem")
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	want := runIMSource(t, "native-slack-socket.fixture.ts", "CC_HAHA_SLACK_SOCKET_FIXTURE", map[string]any{"url": address, "ca": string(ca)}, home, map[string]string{"NODE_EXTRA_CA_CERTS": caPath})
	awaitClosed("Source")
	mu.Lock()
	sourceAcks := append([]string{}, acks...)
	sourceClosed := closed
	acks = nil
	closed = 0
	mu.Unlock()
	if sourceClosed != 2 {
		t.Fatalf("Source did not close both actual peers: %d", sourceClosed)
	}
	roots := x509.NewCertPool()
	roots.AddCert(peer.Certificate())
	var traceMu sync.Mutex
	logs := []string{}
	envelopes := []protocol.JSONObject{}
	opens := 0
	done := make(chan struct{})
	socket := NewSlackSocketMode(context.Background(), SlackSocketOptions{TLS: &tls.Config{RootCAs: roots}, Log: func(s string) {
		if s == "[Slack] Socket parse error" {
			s = "[Slack] Socket parse error: SyntaxError: JSON Parse error: Unexpected identifier \"not\""
		}
		traceMu.Lock()
		logs = append(logs, s)
		traceMu.Unlock()
	}, OpenConnection: func(context.Context) (string, error) {
		opens++
		if opens == 1 {
			return "", errors.New("private open failure")
		}
		if opens == 2 {
			return address + "/one", nil
		}
		return address + "/two", nil
	}, OnEnvelope: func(e protocol.JSONObject) {
		traceMu.Lock()
		envelopes = append(envelopes, e)
		n := len(envelopes)
		traceMu.Unlock()
		if n == 2 {
			close(done)
		}
	}})
	socket.Start()
	select {
	case <-done:
	case <-time.After(7 * time.Second):
		t.Fatal("Go socket did not reconnect")
	}
	time.Sleep(50 * time.Millisecond)
	socket.Close()
	// Local Close joins the runner; remote handlers observe the closed fd on
	// their own schedule. Await those physical events instead of reading their
	// counter immediately. The exact two-peer and ACK assertions remain.
	awaitClosed("Go")
	// Parse diagnostics vary across JS runtimes; the protocol and retry logs are exact.
	var expected protocol.JSONObject
	_ = expected.UnmarshalJSON(want)
	expectedLogs := []string{}
	_ = json.Unmarshal(expected.Get("logs"), &expectedLogs)
	strip := func(rows []string) []string {
		out := []string{}
		for _, row := range rows {
			if !strings.HasPrefix(row, "[Slack] Socket parse error") {
				out = append(out, row)
			}
		}
		return out
	}
	expected.Set("logs", raw(strip(expectedLogs)))
	traceMu.Lock()
	got := raw(map[string]any{"opens": opens, "envelopes": envelopes, "logs": strip(logs)})
	traceMu.Unlock()
	exactJSON(t, got, raw(expected), "actual original WSS producer")
	mu.Lock()
	goAcks := append([]string{}, acks...)
	goClosed := closed
	mu.Unlock()
	exactJSON(t, raw(goAcks), raw(sourceAcks), "physical Source/Go ACKs")
	if goClosed != 2 {
		t.Fatalf("Go Close did not join both peers: %d", goClosed)
	}
	for attempt := 1; attempt < 15; attempt++ {
		if SlackReconnectDelay(attempt) > 30*time.Second {
			t.Fatal("unbounded retry lost original cap")
		}
	}
}
