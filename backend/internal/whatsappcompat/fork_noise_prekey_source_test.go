package whatsappcandidate

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/keys"
)

func TestForkActualFirstCounterTwoNoiseMessageColdLateZeroOneAndPhysicalClose(t *testing.T) {
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	previous := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	defer func() { whatsmeow.WACertPubKey = previous }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first := originalForkEdges(t, map[string]any{"op": "prekey", "message": map[string]any{"conversation": "PRIVATE_COLD_PREKEY_0"}})
	packets := []nodeObject{nodeObj(first["packet"])}
	a := first["a"]
	for i := 1; i <= 2; i++ {
		next := originalMessagePeer(t, map[string]any{"op": "encrypt", "a": a, "message": map[string]any{"conversation": fmt.Sprintf("PRIVATE_COLD_PREKEY_%d", i)}})
		packets = append(packets, nodeObj(next["packet"]))
		a = next["a"]
	}
	b := nodeObj(first["b"])
	creds := nodeObj(b["creds"])
	creds["me"] = nodeJSON(map[string]any{"id": "222:3@s.whatsapp.net", "name": "private-original-peer", "lid": "99222:3@lid"})
	b["creds"] = nodeJSON(creds)
	_, _, dir := forkDeviceFromOriginal(t, b)
	send := make(chan wab.Node, 1)
	connected := make(chan struct{}, 2)
	received := make(chan *events.Message, 3)
	failures := make(chan error, 3)
	closed := make(chan struct{}, 2)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { closed <- struct{}{} }()
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			failures <- err
			return
		}
		defer conn.CloseNow()
		wire, p, err := acceptPrivateNoise(ctx, conn, root, intermediate, static)
		if err != nil {
			failures <- err
			return
		}
		if p.GetUsername() != 222 || p.GetDevice() != 3 {
			failures <- fmt.Errorf("private original login identity mismatch")
			return
		}
		if err = wire.Send(wab.Node{Tag: "success", Attrs: wab.Attrs{"lid": types.JID{User: "99222", Device: 3, Server: types.HiddenUserServer}, "t": time.Now().Unix()}}); err != nil {
			failures <- err
			return
		}
		peerCtx, peerCancel := context.WithCancel(ctx)
		writerDone := make(chan struct{})
		defer func() { peerCancel(); <-writerDone }()
		go func() {
			defer close(writerDone)
			for {
				select {
				case packet := <-send:
					if err := wire.Send(packet); err != nil {
						if peerCtx.Err() == nil {
							failures <- err
						}
						return
					}
				case <-peerCtx.Done():
					return
				}
			}
		}()
		for {
			node, err := wire.Read()
			if err != nil {
				return
			}
			if node.Tag == "iq" {
				reply := wab.Node{Tag: "iq", Attrs: wab.Attrs{"id": node.Attrs["id"], "type": "result", "from": types.ServerJID}}
				if node.GetChildByTag("count").Tag != "" {
					reply.Content = []wab.Node{{Tag: "count", Attrs: wab.Attrs{"value": "50"}}}
				}
				if err = wire.Send(reply); err != nil {
					return
				}
			}
		}
	}))
	defer peer.Close()
	var client *NodeWhatsAppClient
	defer func() {
		if client != nil {
			_ = client.Close(context.Background())
		}
	}()
	start := func() {
		var err error
		client, err = NewNodeWhatsAppClient(ctx, NodeWhatsAppOptions{AuthDir: dir, HTTPTransport: waSocketRoute{peer}, OnEvent: func(v any) {
			switch e := v.(type) {
			case *events.Connected:
				connected <- struct{}{}
			case *events.Message:
				received <- e
			case *events.UndecryptableMessage:
				failures <- fmt.Errorf("actual Noise PreKey %s undecryptable", e.Info.ID)
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err = client.Connect(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case <-connected:
		case err := <-failures:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal("actual Noise connection did not start")
		}
	}
	start()
	for order, index := range []int{2, 0, 1} {
		packet := packets[index]
		if nodeString(packet["type"]) != "pkmsg" {
			t.Fatal("actual original packet is not PreKey", index)
		}
		send <- wab.Node{Tag: "message", Attrs: wab.Attrs{"id": fmt.Sprintf("PRIVATE_INITIAL_LATE_%d", index), "from": types.NewJID("111", types.DefaultUserServer), "type": "text", "t": time.Now().Unix(), "offline": "0"}, Content: []wab.Node{{Tag: "enc", Attrs: wab.Attrs{"type": "pkmsg", "v": "2"}, Content: nodeBytes(packet["ciphertext"])}}}
		select {
		case event := <-received:
			if event.Message.GetConversation() != fmt.Sprintf("PRIVATE_COLD_PREKEY_%d", index) || !event.Info.SourceOffline {
				t.Fatal("actual peer first/late message fields", index)
			}
		case err := <-failures:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal("actual first/late PreKey message missing", index)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "session-111.0.json"))
		if err != nil {
			t.Fatal(err)
		}
		if order == 0 {
			values := nodeObj(b["values"])
			values["session"] = nodeJSON(nodeObject{"111.0": raw})
			b["values"] = nodeJSON(values)
			// A separate actual original Node process consumes the counter0 skipped
			// key persisted by the real Noise event consumer, not a synthetic Cipher.
			proof := originalMessagePeer(t, map[string]any{"op": "decrypt", "a": b, "jid": "111@s.whatsapp.net", "type": "pkmsg", "ciphertext": base64.StdEncoding.EncodeToString(nodeBytes(packets[0]["ciphertext"]))})
			if nodeString(nodeObj(proof["message"])["conversation"]) != "PRIVATE_COLD_PREKEY_0" {
				t.Fatal("original process could not read actual Noise persisted skipped key")
			}
			if err = client.Close(ctx); err != nil {
				t.Fatal(err)
			}
			client = nil
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("first physical Noise Close missing")
			}
			start()
		}
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	client = nil
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("cold physical Noise Close missing")
	}
}
