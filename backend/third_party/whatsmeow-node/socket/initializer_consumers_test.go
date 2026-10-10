package socket

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow/binary/token"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestInitializedHeaderAndSentinelsOnPhysicalFrameSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	frame := make(chan []byte, 1)
	closed := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		kind, raw, err := conn.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		if kind != websocket.MessageBinary {
			t.Error("non-binary frame", kind)
			return
		}
		frame <- raw
		_, _, _ = conn.Read(ctx)
	}))
	defer peer.Close()
	fs := NewFrameSocket(waLog.Noop, peer.Client())
	defer fs.Close(-1)
	expectedHeader := []byte{'W', 'A', 6, token.DictVersion}
	if !bytes.Equal(fs.Header, expectedHeader) || &fs.Header[0] != &WAConnHeader[0] {
		t.Fatal("constructor lost header contents or shared slice")
	}
	for attempt := 0; attempt < 2; attempt++ {
		err := fs.SendFrame([]byte("closed"))
		if err != ErrSocketClosed || err.Error() != "frame socket is closed" {
			t.Fatal("closed socket sentinel", err)
		}
	}
	fs.URL = peer.URL
	if err := fs.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fs.Connect(ctx); err != ErrSocketAlreadyOpen || err.Error() != "frame socket is already open" {
		t.Fatal("already open sentinel", err)
	}
	if err := fs.SendFrame(make([]byte, FrameMaxSize)); !errors.Is(err, ErrFrameTooLarge) || err.Error() != "frame too large (got 16777216 bytes, max 16777216 bytes)" {
		t.Fatal("oversized frame sentinel", err)
	}
	if err := fs.SendFrame([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-frame:
		want := append(bytes.Clone(expectedHeader), 0, 0, 2, 'o', 'k')
		if !bytes.Equal(got, want) {
			t.Fatal("physical peer received wrong header/frame", got, want)
		}
	case <-ctx.Done():
		t.Fatal("physical frame was not delivered", ctx.Err())
	}
	fs.Close(-1)
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("physical peer did not close", ctx.Err())
	}
	if err := fs.SendFrame(nil); err != ErrSocketClosed {
		t.Fatal("closed socket sentinel changed after physical close", err)
	}
	if ErrSocketClosed == ErrSocketAlreadyOpen || ErrSocketClosed == ErrFrameTooLarge {
		t.Fatal("socket sentinels became aliases")
	}
}
