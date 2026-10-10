package imruntime

import (
	"bufio"
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
	"sync"
	"time"
)

type Socket struct {
	conn net.Conn
	read *bufio.Reader
	mu   sync.Mutex
}
type SocketClose struct {
	Code   int
	Reason string
}

func (e *SocketClose) Error() string {
	return fmt.Sprintf("WebSocket closed (%d: %s)", e.Code, e.Reason)
}
func DialSocket(ctx context.Context, address string, tlsConfig *tls.Config) (*Socket, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "wss" {
			port = "443"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
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
		if err = secure.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = secure
	} else if u.Scheme != "ws" {
		return nil, errors.New("invalid WebSocket URL")
	}
	var nonce [16]byte
	if _, err = io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	request := &http.Request{Method: "GET", URL: &url.URL{Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}, Host: u.Host, Header: http.Header{}}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", key)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err = request.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		_ = response.Body.Close()
		return nil, fmt.Errorf("Unexpected server response: %d", response.StatusCode)
	}
	_ = conn.SetDeadline(time.Time{})
	failed = false
	return &Socket{conn: conn, read: reader}, nil
}
func (s *Socket) Send(data []byte) error { return s.frame(1, data) }
func (s *Socket) frame(op byte, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	header := []byte{0x80 | op}
	n := len(data)
	if n < 126 {
		header = append(header, 0x80|byte(n))
	} else if n <= 65535 {
		header = append(header, 0xfe, byte(n>>8), byte(n))
	} else {
		header = append(header, 0xff)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(n))
		header = append(header, size[:]...)
	}
	var mask [4]byte
	if _, err := io.ReadFull(rand.Reader, mask[:]); err != nil {
		return err
	}
	header = append(header, mask[:]...)
	wire := make([]byte, len(data))
	for i, value := range data {
		wire[i] = value ^ mask[i%4]
	}
	if _, err := s.conn.Write(header); err != nil {
		return err
	}
	_, err := s.conn.Write(wire)
	return err
}
func (s *Socket) Read() ([]byte, error) {
	result := []byte{}
	for {
		var head [2]byte
		if _, err := io.ReadFull(s.read, head[:]); err != nil {
			return nil, err
		}
		op := head[0] & 15
		final := head[0]&0x80 != 0
		if head[0]&0x70 != 0 {
			return nil, errors.New("unsupported WebSocket extension frame")
		}
		length := uint64(head[1] & 127)
		if length == 126 {
			var b [2]byte
			if _, err := io.ReadFull(s.read, b[:]); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(b[:]))
		} else if length == 127 {
			var b [8]byte
			if _, err := io.ReadFull(s.read, b[:]); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(b[:])
		}
		if length > 100*1024*1024 {
			return nil, errors.New("Max payload size exceeded")
		}
		var mask [4]byte
		if head[1]&0x80 != 0 {
			if _, err := io.ReadFull(s.read, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, int(length))
		if _, err := io.ReadFull(s.read, payload); err != nil {
			return nil, err
		}
		if head[1]&0x80 != 0 {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch op {
		case 8:
			code := 1005
			reason := ""
			if len(payload) >= 2 {
				code = int(binary.BigEndian.Uint16(payload[:2]))
				reason = string(payload[2:])
			}
			_ = s.frame(8, payload)
			return nil, &SocketClose{code, reason}
		case 9:
			_ = s.frame(10, payload)
			continue
		case 10:
			continue
		case 0, 1, 2:
			result = append(result, payload...)
		default:
			return nil, errors.New("invalid WebSocket opcode")
		}
		if final {
			return result, nil
		}
	}
}
func (s *Socket) Close(reason string) error {
	_ = s.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_ = s.frame(8, append([]byte{3, 232}, []byte(reason)...))
	return s.conn.Close()
}
