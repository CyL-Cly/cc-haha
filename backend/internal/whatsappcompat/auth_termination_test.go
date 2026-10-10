package whatsappcandidate

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/keys"
)

func TestActualNativeLoggedOutPreservesOriginalAuthAndExplicitUnlinkDeletes(t *testing.T) {
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	pin := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	defer func() { whatsmeow.WACertPubKey = pin }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	creds, e := CreateNodeCredentials()
	if e != nil {
		t.Fatal(e)
	}
	creds["me"] = nodeJSON(map[string]any{"id": "222:3@s.whatsapp.net"})
	original := nodeJSON(creds)
	if e = os.WriteFile(filepath.Join(dir, "creds.json"), original, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "old-unknown.json"), []byte(`{"future":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	closed := make(chan struct{})
	peerErrors := make(chan error, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if e != nil {
			peerErrors <- e
			return
		}
		defer conn.CloseNow()
		wire, _, e := acceptPrivateNoise(ctx, conn, root, intermediate, static)
		if e != nil {
			peerErrors <- e
			return
		}
		if e = wire.Send(wab.Node{Tag: "failure", Attrs: wab.Attrs{"reason": "401"}}); e != nil {
			peerErrors <- e
			return
		}
		for {
			if _, e = wire.Read(); e != nil {
				return
			}
		}
	}))
	defer peer.Close()
	loggedOut := make(chan struct{}, 1)
	n, e := NewNodeWhatsAppClient(ctx, NodeWhatsAppOptions{AuthDir: dir, HTTPTransport: waSocketRoute{peer}, OnEvent: func(v any) {
		if _, ok := v.(*events.LoggedOut); ok {
			loggedOut <- struct{}{}
		}
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close(context.Background())
	if e = n.Connect(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-loggedOut:
	case e := <-peerErrors:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("real 401 terminal event missing")
	}
	if e = n.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("terminal physical close missing")
	}
	n.mu.Lock()
	terminal, reconnecting := n.terminal, n.reconnect != nil
	n.mu.Unlock()
	if !terminal || reconnecting {
		t.Fatal("logged-out must not reconnect")
	}
	got, e := os.ReadFile(filepath.Join(dir, "creds.json"))
	if e != nil || !bytes.Equal(got, original) {
		t.Fatal("automatic SDK logout changed original Node auth directory", e)
	}
	if _, e = os.Stat(filepath.Join(dir, "old-unknown.json")); e != nil {
		t.Fatal("unknown old data lost", e)
	}
	if e = n.Store.ClearAuth(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("explicit unlink did not really remove owned directory", e)
	}
}
