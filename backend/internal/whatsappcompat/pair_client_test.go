package whatsappcandidate

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/keys"
	"google.golang.org/protobuf/proto"
)

func TestActualNoisePairSuccessWritesOldCredentialsAndOriginalSourceAccepts(t *testing.T) {
	actualPairSave(t, false)
}
func TestActualNoisePairSuccessSaveFailureOnlyWarnsAndKeepsPairing(t *testing.T) {
	actualPairSave(t, true)
}
func actualPairSave(t *testing.T, saveFailure bool) {
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	oldPin := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	defer func() { whatsmeow.WACertPubKey = oldPin }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	creds, e := CreateNodeCredentials()
	if e != nil {
		t.Fatal(e)
	}
	creds["futureField"] = nodeJSON(map[string]any{"preserve": true})
	if e = os.WriteFile(filepath.Join(dir, "creds.json"), nodeJSON(creds), 0600); e != nil {
		t.Fatal(e)
	}
	identity := keys.NewKeyPair()
	details, _ := proto.Marshal(&waAdv.ADVDeviceIdentity{RawID: proto.Uint32(77), Timestamp: proto.Uint64(123456), KeyIndex: proto.Uint32(4)})
	signature := ecc.CalculateSignature(ecc.NewDjbECPrivateKey(*identity.Priv), append(append([]byte{6, 0}, details...), nodeBytes(nodeObj(creds["signedIdentityKey"])["public"])...))
	accountBytes, _ := proto.Marshal(&waAdv.ADVSignedDeviceIdentity{Details: details, AccountSignatureKey: identity.Pub[:], AccountSignature: signature[:]})
	h := hmac.New(sha256.New, nodeBytes(creds["advSecretKey"]))
	h.Write(accountBytes)
	container, _ := proto.Marshal(&waAdv.ADVSignedDeviceIdentityHMAC{Details: accountBytes, HMAC: h.Sum(nil)})
	reply := make(chan []byte, 1)
	peerErrors := make(chan error, 1)
	peerClosed := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(peerClosed)
		c, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if e != nil {
			peerErrors <- e
			return
		}
		defer c.CloseNow()
		wire, p, e := acceptPrivateNoise(ctx, c, root, intermediate, static)
		if e != nil {
			peerErrors <- e
			return
		}
		if p.GetDevicePairingData() == nil {
			t.Error("actual pairing payload absent")
		}
		e = wire.Send(wab.Node{Tag: "iq", Attrs: wab.Attrs{"id": "private-pair", "type": "set", "from": types.ServerJID, "t": time.Now().Unix()}, Content: []wab.Node{{Tag: "pair-success", Content: []wab.Node{{Tag: "device-identity", Content: container}, {Tag: "device", Attrs: wab.Attrs{"jid": types.NewADJID("222", 0, 3), "lid": types.JID{User: "99222", Server: types.HiddenUserServer, Device: 3}}}, {Tag: "biz", Attrs: wab.Attrs{"name": "Private Business"}}, {Tag: "platform", Attrs: wab.Attrs{"name": "android"}}}}}})
		if e != nil {
			peerErrors <- e
			return
		}
		for {
			node, e := wire.Read()
			if e != nil {
				if ctx.Err() == nil {
					peerErrors <- e
				}
				return
			}
			if child := node.GetChildByTag("pair-device-sign"); child.Tag != "" {
				if b, ok := child.GetChildByTag("device-identity").Content.([]byte); ok {
					reply <- b
				}
			}
		}
	}))
	defer peer.Close()
	var warns atomic.Int32
	pairErrors := make(chan error, 1)
	paired := make(chan struct{}, 1)
	n, e := NewNodeWhatsAppClient(ctx, NodeWhatsAppOptions{AuthDir: dir, HTTPTransport: waSocketRoute{peer}, OnEvent: func(v any) {
		if failed, ok := v.(*events.PairError); ok {
			pairErrors <- failed.Error
		}
		if _, ok := v.(*events.PairSuccess); ok {
			paired <- struct{}{}
		}
	}})
	if e != nil {
		t.Fatal(e)
	}
	n.Store.Warn = func(error) { warns.Add(1) }
	defer n.Close(context.Background())
	if saveFailure {
		if e = n.Store.WaitCredsSave(ctx); e != nil {
			t.Fatal(e)
		}
		if e = os.Remove(filepath.Join(dir, "creds.json")); e != nil {
			t.Fatal(e)
		}
		if e = os.Mkdir(filepath.Join(dir, "creds.json"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e = n.Connect(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-paired:
	case e := <-pairErrors:
		t.Fatal("Source pairing must not fail on asynchronous credential save", e)
	case e := <-peerErrors:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("actual PairSuccess missing")
	}
	var signed []byte
	select {
	case signed = <-reply:
	case e := <-peerErrors:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("actual pair-device-sign missing")
	}
	if saveFailure {
		if warns.Load() != 1 {
			t.Fatal("Source queued save warning missing", warns.Load())
		}
		if e = n.Close(ctx); e != nil {
			t.Fatal(e)
		}
		select {
		case <-peerClosed:
		case <-ctx.Done():
			t.Fatal("physical Close after queued failure missing")
		}
		return
	}
	if e = n.Store.WaitCredsSave(ctx); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "creds.json"))
	if e != nil {
		t.Fatal(e)
	}
	mode, e := os.Stat(filepath.Join(dir, "creds.json"))
	if e != nil || mode.Mode().Perm() != 0600 {
		t.Fatal("credentials mode", e)
	}
	backup, e := os.ReadFile(filepath.Join(dir, "creds.json.bak"))
	if e != nil || !bytes.Equal(backup, nodeJSON(creds)) {
		t.Fatal("actual old backup changed", e)
	}
	home := t.TempDir()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_pair.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(nodeJSON(map[string]any{"old": creds, "actual": nodeObj(raw), "identity": base64.StdEncoding.EncodeToString(container), "reply": base64.StdEncoding.EncodeToString(signed)}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source Pair", e, stderr.String())
	}
	var proof map[string]any
	if e = json.Unmarshal(out, &proof); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(proof["expected"], proof["actual"]) || proof["validNativeSignature"] != true || proof["validReplySignature"] != true {
		t.Fatal("Source Pair shape/signature mismatch")
	}
	cold, _, e := OpenNodeDevice(dir, nil)
	if e != nil || cold.GetJID().String() != "222:3@s.whatsapp.net" || cold.Account == nil {
		t.Fatal("actual paired old-format cold Device", e)
	}
	if e = n.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-peerClosed:
	case <-ctx.Done():
		t.Fatal("actual peer close missing")
	}
}
