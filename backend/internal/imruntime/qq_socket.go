package imruntime

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type qqGatewayNativeSocket struct {
	socket                        *Socket
	compressed, noContextTakeover bool
	dictionary                    []byte
}

func DialQQGatewaySocket(ctx context.Context, address string, headers http.Header, tlsConfig *tls.Config) (QQGatewaySocket, error) {
	u, e := url.Parse(address)
	if e != nil {
		return nil, e
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return nil, errors.New("QQ gateway requires ws or wss URL")
	}
	target := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "wss" {
			port = "443"
		}
		target = net.JoinHostPort(u.Hostname(), port)
	}
	conn, e := (&net.Dialer{}).DialContext(ctx, "tcp", target)
	if e != nil {
		return nil, e
	}
	failed := true
	defer func() {
		if failed {
			_ = conn.Close()
		}
	}()
	if u.Scheme == "wss" {
		config := &tls.Config{}
		if tlsConfig != nil {
			config = tlsConfig.Clone()
		}
		config.ServerName = u.Hostname()
		secure := tls.Client(conn, config)
		if e = secure.HandshakeContext(ctx); e != nil {
			return nil, e
		}
		conn = secure
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return nil, e
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	req := &http.Request{Method: "GET", URL: &url.URL{Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}, Host: u.Host, Header: headers.Clone()}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Extensions", "permessage-deflate; client_max_window_bits")
	if e = req.Write(conn); e != nil {
		return nil, e
	}
	reader := bufio.NewReader(conn)
	res, e := http.ReadResponse(reader, req)
	if e != nil {
		return nil, e
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if res.StatusCode != 101 || res.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		res.Body.Close()
		return nil, fmt.Errorf("Unexpected server response: %d", res.StatusCode)
	}
	extensions := res.Header.Get("Sec-WebSocket-Extensions")
	if extensions != "" && !strings.HasPrefix(extensions, "permessage-deflate") {
		return nil, errors.New("Server indicated an extension that was not requested")
	}
	failed = false
	return &qqGatewayNativeSocket{socket: &Socket{conn: conn, read: reader}, compressed: extensions != "", noContextTakeover: strings.Contains(extensions, "server_no_context_takeover")}, nil
}
func (s *qqGatewayNativeSocket) Send(data []byte) error    { return s.socket.Send(data) }
func (s *qqGatewayNativeSocket) Close(reason string) error { return s.socket.Close(reason) }
func (s *qqGatewayNativeSocket) Read() ([]byte, error) {
	result := []byte{}
	compressed := false
	fragmented := false
	for {
		var head [2]byte
		if _, e := io.ReadFull(s.socket.read, head[:]); e != nil {
			return nil, e
		}
		op := head[0] & 15
		final := head[0]&0x80 != 0
		if head[0]&0x30 != 0 {
			return nil, errors.New("Invalid WebSocket RSV2/RSV3 flag")
		}
		if head[0]&0x40 != 0 {
			if !s.compressed || op == 0 || op >= 8 {
				return nil, errors.New("Invalid WebSocket RSV1 flag")
			}
			compressed = true
		}
		length := uint64(head[1] & 127)
		if length == 126 {
			var b [2]byte
			if _, e := io.ReadFull(s.socket.read, b[:]); e != nil {
				return nil, e
			}
			length = uint64(binary.BigEndian.Uint16(b[:]))
		} else if length == 127 {
			var b [8]byte
			if _, e := io.ReadFull(s.socket.read, b[:]); e != nil {
				return nil, e
			}
			length = binary.BigEndian.Uint64(b[:])
		}
		if length > 100*1024*1024 {
			return nil, errors.New("Max payload size exceeded")
		}
		if head[1]&0x80 != 0 {
			return nil, errors.New("Invalid server WebSocket masked frame")
		}
		if op >= 8 && (!final || length > 125) {
			return nil, errors.New("Invalid WebSocket control frame")
		}
		body := make([]byte, int(length))
		if _, e := io.ReadFull(s.socket.read, body); e != nil {
			return nil, e
		}
		switch op {
		case 8:
			code := 1005
			reason := ""
			if len(body) >= 2 {
				code = int(binary.BigEndian.Uint16(body))
				reason = string(protocol.DecodeJavaScriptUTF8(body[2:]))
			}
			_ = s.socket.frame(8, body)
			return nil, &SocketClose{code, reason}
		case 9:
			_ = s.socket.frame(10, body)
			continue
		case 10:
			continue
		case 0:
			if !fragmented {
				return nil, errors.New("Unexpected WebSocket continuation")
			}
		case 1, 2:
			if fragmented {
				return nil, errors.New("Invalid WebSocket fragmented message")
			}
			fragmented = !final
		default:
			return nil, errors.New("Invalid WebSocket opcode")
		}
		result = append(result, body...)
		if !final {
			fragmented = true
			continue
		}
		if !compressed {
			return result, nil
		}
		encoded := append(append([]byte{}, result...), 0, 0, 255, 255, 1, 0, 0, 255, 255)
		reader := flate.NewReaderDict(bytes.NewReader(encoded), s.dictionary)
		decoded, e := io.ReadAll(reader)
		reader.Close()
		if e != nil {
			return nil, e
		}
		if !s.noContextTakeover {
			s.dictionary = append(s.dictionary, decoded...)
			if len(s.dictionary) > 32768 {
				s.dictionary = append([]byte{}, s.dictionary[len(s.dictionary)-32768:]...)
			}
		}
		return decoded, nil
	}
}
