package whatsappcandidate

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"github.com/coder/websocket"
	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waCert"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/keys"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type waSocketRoute struct{ peer *httptest.Server }

func (r waSocketRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "web.whatsapp.com" {
		return nil, fmt.Errorf("unowned WhatsApp socket target")
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "http"
	u.Host = r.peer.Listener.Addr().String()
	copy.URL = &u
	return r.peer.Client().Transport.RoundTrip(copy)
}
func noiseAgreement(private, public [32]byte) ([]byte, error) {
	key, e := ecdh.X25519().NewPrivateKey(private[:])
	if e != nil {
		return nil, e
	}
	pub, e := ecdh.X25519().NewPublicKey(public[:])
	if e != nil {
		return nil, e
	}
	return key.ECDH(pub)
}
func actualSignedNoiseChain(root, intermediate, static *keys.KeyPair) []byte {
	details, _ := proto.Marshal(&waCert.CertChain_NoiseCertificate_Details{Serial: proto.Uint32(1), IssuerSerial: proto.Uint32(0), Key: intermediate.Pub[:]})
	sig := ecc.CalculateSignature(ecc.NewDjbECPrivateKey(*root.Priv), details)
	leafDetails, _ := proto.Marshal(&waCert.CertChain_NoiseCertificate_Details{Serial: proto.Uint32(2), IssuerSerial: proto.Uint32(1), Key: static.Pub[:]})
	leafSig := ecc.CalculateSignature(ecc.NewDjbECPrivateKey(*intermediate.Priv), leafDetails)
	cert, _ := proto.Marshal(&waCert.CertChain{Intermediate: &waCert.CertChain_NoiseCertificate{Details: details, Signature: sig[:]}, Leaf: &waCert.CertChain_NoiseCertificate{Details: leafDetails, Signature: leafSig[:]}})
	return cert
}
func TestActualPinnedWhatsmeowSignedNoiseHandshakePrivateQRAndPhysicalClose(t *testing.T) {
	actualNoiseClient(t, false)
}
func TestActualPinnedWhatsmeowRestoresOldNodeCredentialsNoiseAndPhysicalClose(t *testing.T) {
	actualNoiseClient(t, true)
}
func actualNoiseClient(t *testing.T, restore bool) {
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	originalPin := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	defer func() { whatsmeow.WACertPubKey = originalPin }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	closed := make(chan struct{})
	payloadSeen := make(chan *waWa6.ClientPayload, 1)
	peerErr := make(chan error, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if e != nil {
			peerErr <- e
			return
		}
		defer conn.CloseNow()
		defer close(closed)
		fail := func(e error) { peerErr <- e }
		_, frame, e := conn.Read(ctx)
		if e != nil {
			fail(e)
			return
		}
		header, helloRaw, e := parseNoiseFrame(frame, true)
		if e != nil {
			fail(e)
			return
		}
		var hello waWa6.HandshakeMessage
		if e = proto.Unmarshal(helloRaw, &hello); e != nil {
			fail(e)
			return
		}
		clientPub := hello.GetClientHello().GetEphemeral()
		if len(clientPub) != 32 {
			fail(fmt.Errorf("missing actual client ephemeral"))
			return
		}
		eph := keys.NewKeyPair()
		noise := newNoisePeer(socket.NoiseStartPattern, header)
		noise.auth(clientPub)
		noise.auth(eph.Pub[:])
		secret, e := noiseAgreement(*eph.Priv, *(*[32]byte)(clientPub))
		if e != nil {
			fail(e)
			return
		}
		if e = noise.mix(secret); e != nil {
			fail(e)
			return
		}
		staticCipher := noise.encrypt(static.Pub[:])
		secret, e = noiseAgreement(*static.Priv, *(*[32]byte)(clientPub))
		if e != nil {
			fail(e)
			return
		}
		if e = noise.mix(secret); e != nil {
			fail(e)
			return
		}
		certCipher := noise.encrypt(actualSignedNoiseChain(root, intermediate, static))
		serverHello, _ := proto.Marshal(&waWa6.HandshakeMessage{ServerHello: &waWa6.HandshakeMessage_ServerHello{Ephemeral: eph.Pub[:], Static: staticCipher, Payload: certCipher}})
		if e = conn.Write(ctx, websocket.MessageBinary, noiseFrame(serverHello)); e != nil {
			fail(e)
			return
		}
		_, frame, e = conn.Read(ctx)
		if e != nil {
			fail(e)
			return
		}
		_, finishRaw, e := parseNoiseFrame(frame, false)
		if e != nil {
			fail(e)
			return
		}
		var finish waWa6.HandshakeMessage
		if e = proto.Unmarshal(finishRaw, &finish); e != nil {
			fail(e)
			return
		}
		clientStatic, e := noise.decrypt(finish.GetClientFinish().GetStatic())
		if e != nil {
			fail(e)
			return
		}
		secret, e = noiseAgreement(*eph.Priv, *(*[32]byte)(clientStatic))
		if e != nil {
			fail(e)
			return
		}
		if e = noise.mix(secret); e != nil {
			fail(e)
			return
		}
		payload, e := noise.decrypt(finish.GetClientFinish().GetPayload())
		if e != nil {
			fail(e)
			return
		}
		var clientPayload waWa6.ClientPayload
		if e = proto.Unmarshal(payload, &clientPayload); e != nil {
			fail(e)
			return
		}
		payloadSeen <- &clientPayload
		_, serverWrite, e := noise.split()
		if e != nil {
			fail(e)
			return
		}
		node, e := wab.Marshal(wab.Node{Tag: "iq", Attrs: wab.Attrs{"id": "private-qr", "type": "set", "from": types.ServerJID}, Content: []wab.Node{{Tag: "pair-device", Content: []wab.Node{{Tag: "ref", Content: []byte("private-qr-reference")}}}}})
		if e != nil {
			fail(e)
			return
		}
		iv := make([]byte, 12)
		binary.BigEndian.PutUint32(iv[8:], 0)
		encrypted := serverWrite.Seal(nil, iv, node, nil)
		if e = conn.Write(ctx, websocket.MessageBinary, noiseFrame(encrypted)); e != nil {
			fail(e)
			return
		}
		for {
			if _, _, e = conn.Read(ctx); e != nil {
				return
			}
		}
	}))
	defer peer.Close()
	creds, e := CreateNodeCredentials()
	if restore {
		source := actualNodePeer(t, map[string]any{"op": "setup"})
		creds = nodeObj(nodeObj(source["b"])["creds"])
		creds["me"] = nodeJSON(map[string]any{"id": "222:3@s.whatsapp.net", "name": "private-old-user"})
		creds["registered"] = nodeJSON(true)
	}
	if e != nil {
		t.Fatal(e)
	}
	device, e := DeviceFromNodeCredentials(creds)
	if e != nil {
		t.Fatal(e)
	}
	client := whatsmeow.NewClient(device, nil)
	client.EnableAutoReconnect = false
	client.InitialAutoReconnect = false
	client.SetPreLoginHTTPClient(&http.Client{Transport: waSocketRoute{peer}})
	client.SetWebsocketHTTPClient(&http.Client{Transport: waSocketRoute{peer}})
	qr := make(chan []string, 1)
	client.AddEventHandler(func(v any) {
		if event, ok := v.(*events.QR); ok {
			qr <- event.Codes
		}
	})
	if e = client.ConnectContext(ctx); e != nil {
		t.Fatal("actual signed Noise handshake", e)
	}
	select {
	case p := <-payloadSeen:
		if !restore && p.GetDevicePairingData() == nil {
			t.Fatal("actual unpaired payload missing pairing data")
		}
		if restore && (p.GetDevicePairingData() != nil || p.GetUsername() != 222 || p.GetDevice() != 3) {
			t.Fatal("old credential login payload differs")
		}
	case e := <-peerErr:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("physical ClientFinish absent")
	}
	select {
	case codes := <-qr:
		want := strings.Join([]string{"private-qr-reference", base64.StdEncoding.EncodeToString(device.NoiseKey.Pub[:]), base64.StdEncoding.EncodeToString(device.IdentityKey.Pub[:]), base64.StdEncoding.EncodeToString(device.AdvSecretKey)}, ",")
		if len(codes) != 1 || codes[0] != want {
			t.Fatal("actual QR event differs")
		}
	case e := <-peerErr:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("actual QR event missing")
	}
	if !client.IsConnected() || client.IsLoggedIn() {
		t.Fatal("physical unpaired state incorrectly reported")
	}
	client.Disconnect()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("physical Noise socket close not joined")
	}
}
