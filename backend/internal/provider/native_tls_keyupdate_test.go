//go:build darwin

package provider

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// A TLS 1.3 test peer uses secrets from the standard private-fixture KeyLogWriter
// to emit the RFC 8446 KeyUpdate which crypto/tls doesn't expose as a public
// server operation. Production encryption/decryption remains the actual tls.Conn.
type tls13ProbeTraffic struct {
	secret []byte
	hash   func() hash.Hash
	keyLen int
	seq    uint64
}

// The raw RFC peer takes over after the real server handshake. Bound each
// handshake Read to one header/body so tls.Conn cannot retain application
// records in its private read-ahead buffer across that explicit test handoff.
type tls13ProbeHandshakeConn struct {
	net.Conn
	header    [5]byte
	position  int
	remaining int
}

func (connection *tls13ProbeHandshakeConn) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	if connection.position < len(connection.header) {
		count, err := connection.Conn.Read(out[:min(len(out), len(connection.header)-connection.position)])
		copy(connection.header[connection.position:], out[:count])
		connection.position += count
		if connection.position == len(connection.header) {
			connection.remaining = int(binary.BigEndian.Uint16(connection.header[3:]))
		}
		return count, err
	}
	count, err := connection.Conn.Read(out[:min(len(out), connection.remaining)])
	connection.remaining -= count
	if connection.remaining == 0 {
		connection.position = 0
	}
	return count, err
}

func tls13ProbeTrafficFromLog(log, label string, suite uint16) (tls13ProbeTraffic, error) {
	traffic := tls13ProbeTraffic{hash: sha256.New, keyLen: 16}
	if suite == tls.TLS_AES_256_GCM_SHA384 {
		traffic.hash, traffic.keyLen = sha512.New384, 32
	} else if suite != tls.TLS_AES_128_GCM_SHA256 {
		return traffic, fmt.Errorf("private KeyUpdate peer requires negotiated AES-GCM, got %x", suite)
	}
	for _, line := range strings.Split(log, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == label {
			var err error
			traffic.secret, err = hex.DecodeString(fields[2])
			return traffic, err
		}
	}
	return traffic, fmt.Errorf("private fixture key-log traffic label absent")
}

func (traffic *tls13ProbeTraffic) expand(label string, count int) ([]byte, error) {
	label = "tls13 " + label
	info := []byte{byte(count >> 8), byte(count), byte(len(label))}
	info = append(info, label...)
	info = append(info, 0)
	return hkdf.Expand(traffic.hash, traffic.secret, string(info), count)
}

func (traffic *tls13ProbeTraffic) aeadNonce() (cipher.AEAD, []byte, error) {
	key, err := traffic.expand("key", traffic.keyLen)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := traffic.expand("iv", aead.NonceSize())
	if err != nil {
		return nil, nil, err
	}
	for i := range 8 {
		nonce[len(nonce)-1-i] ^= byte(traffic.seq >> (i * 8))
	}
	return aead, nonce, nil
}

func (traffic *tls13ProbeTraffic) next() error {
	secret, err := traffic.expand("traffic upd", traffic.hash().Size())
	if err == nil {
		traffic.secret, traffic.seq = secret, 0
	}
	return err
}

func (traffic *tls13ProbeTraffic) write(peer io.Writer, content []byte, kind byte) error {
	aead, nonce, err := traffic.aeadNonce()
	if err != nil {
		return err
	}
	plain := append(append([]byte(nil), content...), kind)
	length := len(plain) + aead.Overhead()
	header := []byte{23, 3, 3, byte(length >> 8), byte(length)}
	wire := append(header, aead.Seal(nil, nonce, plain, header)...)
	if _, err = peer.Write(wire); err == nil {
		traffic.seq++
	}
	return err
}

func (traffic *tls13ProbeTraffic) read(peer io.Reader) ([]byte, byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(peer, header[:]); err != nil {
		return nil, 0, err
	}
	if header[0] != 23 || header[1] != 3 || header[2] != 3 {
		return nil, 0, fmt.Errorf("unexpected physical encrypted record header %x", header)
	}
	encoded := make([]byte, int(binary.BigEndian.Uint16(header[3:])))
	if _, err := io.ReadFull(peer, encoded); err != nil {
		return nil, 0, err
	}
	aead, nonce, err := traffic.aeadNonce()
	if err != nil {
		return nil, 0, err
	}
	plain, err := aead.Open(nil, nonce, encoded, header[:])
	if err != nil {
		return nil, 0, err
	}
	traffic.seq++
	for len(plain) > 0 && plain[len(plain)-1] == 0 {
		plain = plain[:len(plain)-1]
	}
	if len(plain) == 0 {
		return nil, 0, fmt.Errorf("empty decrypted record content type")
	}
	return plain[:len(plain)-1], plain[len(plain)-1], nil
}

func TestNativeTLSActualKeyUpdateDuringBlockedRequestKeepsEncryptedFIFO(t *testing.T) {
	serverConfig, trust := tlsProbeConfiguration(t)
	serverConfig.MinVersion, trust.MinVersion = tls.VersionTLS13, tls.VersionTLS13
	serverConfig.SessionTicketsDisabled = true
	var keyLog bytes.Buffer
	serverConfig.KeyLogWriter = &keyLog
	listener := tlsProbeListener(t)
	keyUpdate := make(chan struct{})
	keySent := make(chan struct{})
	readRelease := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(readRelease) }) }
	defer release()
	peerDone := make(chan error, 1)
	wire := bytes.Repeat([]byte("same verified TLS request bytes 😀\n"), 120000)
	extra := []byte("owner write after actual requested TLS KeyUpdate")
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			peerDone <- err
			return
		}
		defer peer.Close()
		_ = peer.SetDeadline(time.Now().Add(8 * time.Second))
		_ = peer.(*net.TCPConn).SetReadBuffer(4096)
		secured := tls.Server(&tls13ProbeHandshakeConn{Conn: peer}, serverConfig)
		if err = secured.Handshake(); err != nil {
			peerDone <- err
			return
		}
		suite := secured.ConnectionState().CipherSuite
		out, err := tls13ProbeTrafficFromLog(keyLog.String(), "SERVER_TRAFFIC_SECRET_0", suite)
		if err != nil {
			peerDone <- err
			return
		}
		in, err := tls13ProbeTrafficFromLog(keyLog.String(), "CLIENT_TRAFFIC_SECRET_0", suite)
		if err != nil {
			peerDone <- err
			return
		}
		<-keyUpdate
		if err = out.write(peer, []byte{24, 0, 0, 1, 1}, 22); err != nil {
			peerDone <- err
			return
		}
		close(keySent)
		<-readRelease
		var actual bytes.Buffer
		for actual.Len() < len(wire) {
			chunk, kind, err := in.read(peer)
			if err != nil || kind != 23 {
				peerDone <- fmt.Errorf("original encrypted FIFO: type %d: %w", kind, err)
				return
			}
			actual.Write(chunk)
		}
		if !bytes.Equal(actual.Bytes(), wire) {
			peerDone <- fmt.Errorf("original request changed before KeyUpdate ack")
			return
		}
		ack, kind, err := in.read(peer)
		if err != nil || kind != 22 || !bytes.Equal(ack, []byte{24, 0, 0, 1, 0}) {
			peerDone <- fmt.Errorf("actual key update acknowledgement/order: type %d: %w", kind, err)
			return
		}
		if err = in.next(); err != nil {
			peerDone <- err
			return
		}
		actualExtra, kind, err := in.read(peer)
		if err != nil || kind != 23 || !bytes.Equal(actualExtra, extra) {
			peerDone <- fmt.Errorf("actual updated-key application bytes: type %d: %w", kind, err)
			return
		}
		if err = out.next(); err == nil {
			err = out.write(peer, []byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nsame"), 23)
		}
		if err == nil {
			_, err = io.Copy(io.Discard, peer)
		}
		peerDone <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	resource, err := newNativeTLSHTTP1Reservation(ctx, listener.Addr().String(), wire, 128, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer resource.Close()
	select {
	case <-resource.available:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	resource.mu.Lock()
	operation, failure := resource.operation, resource.failure
	resource.mu.Unlock()
	if operation == nil || failure != nil {
		t.Fatal("actual TLS connection", failure)
	}
	stream := operation.recordReader.(*nativeTLSHTTP1Stream)
	writer := stream.wrapped.writer
	for {
		writer.mu.Lock()
		pending, changed := writer.pending, writer.changed
		writer.mu.Unlock()
		if pending > 1024*1024 {
			break
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("original write backpressure", ctx.Err())
		}
	}
	close(keyUpdate)
	select {
	case <-keySent:
	case err = <-peerDone:
		t.Fatal("physical KeyUpdate send", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = resource.WaitReadable(ctx); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, ready, err := resource.Checkpoint(); err != nil || ready {
		t.Fatal("actual owner KeyUpdate read", ready, err)
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatal("owner waited for writable KeyUpdate acknowledgement")
	}
	if count, err := stream.secured.Write(extra); err != nil || count != len(extra) {
		t.Fatal("write after actual crypto/tls key transition", count, err)
	}
	release()
	for {
		if err = resource.WaitReadable(ctx); err != nil {
			t.Fatal(err)
		}
		_, ready, err := resource.Checkpoint()
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
	}
	request, _ := http.NewRequest("POST", "https://"+listener.Addr().String()+"/original", nil)
	response, err := resource.AdoptResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "same" {
		t.Fatal("actual updated-key response", string(body), err)
	}
	if err = resource.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-peerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("actual TLS peer not joined", ctx.Err())
	}
}
