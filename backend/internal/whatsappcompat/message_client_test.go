package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/keys"
)

func originalMessagePeer(t *testing.T, input any) nodeObject {
	t.Helper()
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_message.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(nodeJSON(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("actual original message peer", e, stderr.String())
	}
	return nodeObj(out)
}

func TestActualOldCredentialsNoiseSignalMessageReplyReconnectAndColdRestore(t *testing.T) {
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	oldPin := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	defer func() { whatsmeow.WACertPubKey = oldPin }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	b := nodeObj(source["b"])
	creds := nodeObj(b["creds"])
	creds["me"] = nodeJSON(map[string]any{"id": "222:3@s.whatsapp.net", "name": "private-old-name", "lid": "99222:3@lid"})
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "creds.json"), nodeJSON(creds), 0600); e != nil {
		t.Fatal(e)
	}
	for name, v := range nodeObj(nodeObj(b["values"])["session"]) {
		if e := os.WriteFile(filepath.Join(dir, "session-"+name+".json"), v, 0600); e != nil {
			t.Fatal(e)
		}
	}
	send := make(chan wab.Node, 4)
	sent := make(chan wab.Node, 4)
	connected := make(chan struct{}, 4)
	messages := make(chan *events.Message, 4)
	errorsSeen := make(chan error, 4)
	disconnect := make(chan struct{}, 4)
	closed := make(chan struct{}, 4)
	var connections atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		defer func() { closed <- struct{}{} }()
		conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if e != nil {
			errorsSeen <- e
			return
		}
		defer conn.CloseNow()
		wire, p, e := acceptPrivateNoise(ctx, conn, root, intermediate, static)
		if e != nil {
			errorsSeen <- e
			return
		}
		if p.GetUsername() != 222 || p.GetDevice() != 3 {
			errorsSeen <- fmt.Errorf("old credential login mismatch")
			return
		}
		if e = wire.Send(wab.Node{Tag: "success", Attrs: wab.Attrs{"lid": types.JID{User: "99222", Device: 3, Server: types.HiddenUserServer}, "t": time.Now().Unix()}}); e != nil {
			errorsSeen <- e
			return
		}
		peerCtx, peerCancel := context.WithCancel(ctx)
		defer peerCancel()
		go func() {
			for {
				select {
				case node := <-send:
					if e := wire.Send(node); e != nil {
						if peerCtx.Err() == nil {
							errorsSeen <- e
						}
						return
					}
				case <-disconnect:
					conn.CloseNow()
					return
				case <-peerCtx.Done():
					return
				}
			}
		}()
		for {
			node, e := wire.Read()
			if e != nil {
				return
			}
			switch node.Tag {
			case "iq":
				result := wab.Node{Tag: "iq", Attrs: wab.Attrs{"id": node.Attrs["id"], "type": "result", "from": types.ServerJID}}
				if node.GetChildByTag("count").Tag != "" {
					result.Content = []wab.Node{{Tag: "count", Attrs: wab.Attrs{"value": "50"}}}
				}
				if usync := node.GetChildByTag("usync"); usync.Tag != "" {
					users := []wab.Node{}
					list := usync.GetChildByTag("list")
					for _, u := range list.GetChildren() {
						jid, _ := u.Attrs["jid"].(types.JID)
						device := 0
						if jid.User == "222" || jid.User == "99222" {
							device = 3
						}
						users = append(users, wab.Node{Tag: "user", Attrs: wab.Attrs{"jid": jid}, Content: []wab.Node{{Tag: "devices", Content: []wab.Node{{Tag: "device-list", Content: []wab.Node{{Tag: "device", Attrs: wab.Attrs{"id": device, "key-index": "1"}}}}}}}})
					}
					result.Content = []wab.Node{{Tag: "usync", Content: []wab.Node{{Tag: "list", Content: users}}}}
				}
				if e = wire.Send(result); e != nil {
					return
				}
			case "message":
				sent <- *node
				if e = wire.Send(wab.Node{Tag: "ack", Attrs: wab.Attrs{"id": node.Attrs["id"], "class": "message", "from": node.Attrs["to"], "t": time.Now().Unix()}}); e != nil {
					return
				}
			}
		}
	}))
	defer peer.Close()
	newClient := func() *NodeWhatsAppClient {
		n, e := NewNodeWhatsAppClient(ctx, NodeWhatsAppOptions{AuthDir: dir, HTTPTransport: waSocketRoute{peer}, OnEvent: func(v any) {
			switch event := v.(type) {
			case *events.Connected:
				connected <- struct{}{}
			case *events.Message:
				messages <- event
			case *events.UndecryptableMessage:
				errorsSeen <- fmt.Errorf("native undecryptable %s", event.Info.ID)
			}
		}})
		if e != nil {
			t.Fatal(e)
		}
		if e = n.Connect(ctx); e != nil {
			t.Fatal(e)
		}
		select {
		case <-connected:
		case e := <-errorsSeen:
			t.Fatal(e)
		case <-ctx.Done():
			t.Fatal("connected missing")
		}
		return n
	}
	n := newClient()
	defer func() {
		if n != nil {
			_ = n.Close(context.Background())
		}
	}()
	a := source["a"]
	for round := 0; round < 2; round++ {
		inputText := fmt.Sprintf("PRIVATE_NODE_REAL_MESSAGE_%d", round)
		source = originalMessagePeer(t, map[string]any{"op": "encrypt", "a": a, "message": map[string]any{"conversation": inputText}})
		a = source["a"]
		packet := nodeObj(source["packet"])
		send <- wab.Node{Tag: "message", Attrs: wab.Attrs{"id": fmt.Sprintf("PRIVATE_NODE_ID_%d", round), "from": types.NewJID("111", types.DefaultUserServer), "type": "text", "t": time.Now().Unix(), "notify": "Private Peer"}, Content: []wab.Node{{Tag: "enc", Attrs: wab.Attrs{"v": "2", "type": nodeString(packet["type"])}, Content: nodeBytes(packet["ciphertext"])}}}
		select {
		case event := <-messages:
			if event.Message.GetConversation() != inputText || event.Info.ID != fmt.Sprintf("PRIVATE_NODE_ID_%d", round) || event.Info.Chat.User != "111" {
				t.Fatal("native decrypted message fields differ")
			}
		case e := <-errorsSeen:
			t.Fatal(e)
		case <-ctx.Done():
			t.Fatal("real decrypted Message missing")
		}
		result, e := n.SendText(ctx, types.NewJID("111", types.DefaultUserServer), fmt.Sprintf("PRIVATE_GO_REAL_REPLY_%d", round))
		if e != nil {
			t.Fatal("actual SendMessage", e)
		}
		if result.ID == "" {
			t.Fatal("native message id absent")
		}
		var outbound wab.Node
		select {
		case outbound = <-sent:
		case e := <-errorsSeen:
			t.Fatal(e)
		case <-ctx.Done():
			t.Fatal("actual encrypted reply missing")
		}
		var enc wab.Node
		participants := outbound.GetChildByTag("participants")
		for _, p := range participants.GetChildren() {
			jid, _ := p.Attrs["jid"].(types.JID)
			if jid.User == "111" {
				enc = p.GetChildByTag("enc")
			}
		}
		raw, ok := enc.Content.([]byte)
		if !ok {
			t.Fatal("actual encrypted direct participant absent")
		}
		source = originalMessagePeer(t, map[string]any{"op": "decrypt", "a": a, "type": enc.Attrs["type"], "ciphertext": base64.StdEncoding.EncodeToString(raw)})
		a = source["a"]
		if nodeString(nodeObj(source["message"])["conversation"]) != fmt.Sprintf("PRIVATE_GO_REAL_REPLY_%d", round) {
			t.Fatal("actual original Node could not decrypt response")
		}
		if round == 0 {
			disconnect <- struct{}{}
			select {
			case <-connected:
			case e := <-errorsSeen:
				t.Fatal(e)
			case <-ctx.Done():
				t.Fatal("Source-policy actual reconnect missing")
			}
			if connections.Load() != 2 {
				t.Fatal("reconnect count", connections.Load())
			}
			if e = n.Close(ctx); e != nil {
				t.Fatal(e)
			}
			n = newClient()
		}
	}
	if e := n.Close(ctx); e != nil {
		t.Fatal(e)
	}
	n = nil
	for i := 0; i < 3; i++ {
		select {
		case <-closed:
		case <-ctx.Done():
			t.Fatal("physical peer Close missing")
		}
	}
	if connections.Load() != 3 {
		t.Fatal("cold connection count", connections.Load())
	}
	data, e := os.ReadFile(filepath.Join(dir, "session-111.0.json"))
	if e != nil {
		t.Fatal(e)
	}
	if len(nodeObj(nodeObj(data)["_sessions"])) == 0 {
		t.Fatal("actual old session not persisted")
	}
	t.Log("actual old login, original Node encrypted protobuf Message, native SendMessage/cached commit, original Node decryption, real reconnect, cold restore, physical Close")
}
