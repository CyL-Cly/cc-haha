package application

import (
	"context"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/coder/websocket"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/util/keys"
	"google.golang.org/protobuf/proto"
)

type privateNoiseWire struct {
	ctx                       context.Context
	conn                      *websocket.Conn
	read, write               cipher.AEAD
	readCounter, writeCounter uint32
	writeMu                   sync.Mutex
}

func acceptPrivateNoise(ctx context.Context, conn *websocket.Conn, root, intermediate, static *keys.KeyPair) (*privateNoiseWire, *waWa6.ClientPayload, error) {
	_, frame, e := conn.Read(ctx)
	if e != nil {
		return nil, nil, e
	}
	header, raw, e := parseNoiseFrame(frame, true)
	if e != nil {
		return nil, nil, e
	}
	var hello waWa6.HandshakeMessage
	if e = proto.Unmarshal(raw, &hello); e != nil {
		return nil, nil, e
	}
	clientPub := hello.GetClientHello().GetEphemeral()
	if len(clientPub) != 32 {
		return nil, nil, fmt.Errorf("missing actual client ephemeral")
	}
	eph := keys.NewKeyPair()
	noise := newNoisePeer(socket.NoiseStartPattern, header)
	noise.auth(clientPub)
	noise.auth(eph.Pub[:])
	secret, e := noiseAgreement(*eph.Priv, *(*[32]byte)(clientPub))
	if e != nil {
		return nil, nil, e
	}
	if e = noise.mix(secret); e != nil {
		return nil, nil, e
	}
	staticCipher := noise.encrypt(static.Pub[:])
	secret, e = noiseAgreement(*static.Priv, *(*[32]byte)(clientPub))
	if e != nil {
		return nil, nil, e
	}
	if e = noise.mix(secret); e != nil {
		return nil, nil, e
	}
	certCipher := noise.encrypt(actualSignedNoiseChain(root, intermediate, static))
	server, _ := proto.Marshal(&waWa6.HandshakeMessage{ServerHello: &waWa6.HandshakeMessage_ServerHello{Ephemeral: eph.Pub[:], Static: staticCipher, Payload: certCipher}})
	if e = conn.Write(ctx, websocket.MessageBinary, noiseFrame(server)); e != nil {
		return nil, nil, e
	}
	_, frame, e = conn.Read(ctx)
	if e != nil {
		return nil, nil, e
	}
	_, raw, e = parseNoiseFrame(frame, false)
	if e != nil {
		return nil, nil, e
	}
	var finish waWa6.HandshakeMessage
	if e = proto.Unmarshal(raw, &finish); e != nil {
		return nil, nil, e
	}
	clientStatic, e := noise.decrypt(finish.GetClientFinish().GetStatic())
	if e != nil {
		return nil, nil, e
	}
	if len(clientStatic) != 32 {
		return nil, nil, fmt.Errorf("invalid static")
	}
	secret, e = noiseAgreement(*eph.Priv, *(*[32]byte)(clientStatic))
	if e != nil {
		return nil, nil, e
	}
	if e = noise.mix(secret); e != nil {
		return nil, nil, e
	}
	data, e := noise.decrypt(finish.GetClientFinish().GetPayload())
	if e != nil {
		return nil, nil, e
	}
	var payload waWa6.ClientPayload
	if e = proto.Unmarshal(data, &payload); e != nil {
		return nil, nil, e
	}
	read, write, e := noise.split()
	if e != nil {
		return nil, nil, e
	}
	return &privateNoiseWire{ctx: ctx, conn: conn, read: read, write: write}, &payload, nil
}
func (w *privateNoiseWire) Send(node wab.Node) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	raw, e := wab.Marshal(node)
	if e != nil {
		return e
	}
	iv := make([]byte, 12)
	binary.BigEndian.PutUint32(iv[8:], w.writeCounter)
	w.writeCounter++
	return w.conn.Write(w.ctx, websocket.MessageBinary, noiseFrame(w.write.Seal(nil, iv, raw, nil)))
}
func (w *privateNoiseWire) Read() (*wab.Node, error) {
	_, frame, e := w.conn.Read(w.ctx)
	if e != nil {
		return nil, e
	}
	_, raw, e := parseNoiseFrame(frame, false)
	if e != nil {
		return nil, e
	}
	iv := make([]byte, 12)
	binary.BigEndian.PutUint32(iv[8:], w.readCounter)
	w.readCounter++
	plain, e := w.read.Open(nil, iv, raw, nil)
	if e != nil {
		return nil, e
	}
	plain, e = wab.Unpack(plain)
	if e != nil {
		return nil, e
	}
	return wab.Unmarshal(plain)
}
