package server

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/publicaccess"
	"golang.org/x/net/websocket"
)

func (server *Server) newPublicAccess() {
	server.publicAccess = publicaccess.New(publicaccess.Dependencies{
		Authorization:   server.options.Authorization,
		StorePath:       filepath.Join(server.options.ConfigDir, "cc-haha", "public-access-devices.json"),
		BusinessAllowed: publicaccess.BusinessAllowed,
		Static:          http.HandlerFunc(server.static),
		Business: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.serveAPILifecycle(w, r, false, func(w http.ResponseWriter, r *http.Request) {
				parts := segments(r.URL.EscapedPath())
				if len(parts) < 2 {
					writeJSON(w, 404, map[string]string{"error": "Not found"})
					return
				}
				// This callback is owned exclusively by the paired-device listener.
				// No public header or local bearer can select this trust boundary.
				if parts[1] == "providers" || parts[1] == "settings" {
					server.remoteSettingsHTTP(w, r, parts)
				} else {
					server.serveAPI(w, r, parts)
				}
			})
		}),
		Websocket: server.publicWebsocket,
	})
}

func (server *Server) publicWebsocket(w http.ResponseWriter, r *http.Request, device string) bool {
	// x/net hijacks before validating the opening handshake. Reject these
	// HTTP failures first so the public listener can return Source's JSON.
	header := func(key string) string { return strings.TrimSpace(strings.Join(r.Header.Values(key), ", ")) }
	upgradeConnection := false
	for _, token := range strings.Split(header("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			upgradeConnection = true
		}
	}
	if r.Method != "GET" || !strings.EqualFold(header("Upgrade"), "websocket") || !upgradeConnection || header("Sec-WebSocket-Version") != "13" || header("Sec-WebSocket-Key") == "" {
		return false
	}
	id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if !sessionIDPattern.MatchString(id) {
		return false
	}
	upgrade := &publicUpgradeWriter{ResponseWriter: w, header: http.Header{}}
	websocket.Server{Handshake: func(_ *websocket.Config, _ *http.Request) error { return nil }, Handler: func(ws *websocket.Conn) {
		ws.MaxPayloadBytes = 1024 * 1024
		socket := &connection{ws: ws}
		if !server.publicAccess.RegisterSocket(device, socket) {
			return
		}
		defer server.publicAccess.UnregisterSocket(socket)
		server.connectSocket(id, socket, func() bool { return server.publicAccess.CheckMessage(device, socket) })
	}}.ServeHTTP(upgrade, r)
	return upgrade.hijacked
}

// Keep the Source public listener's JSON "Upgrade failed" response even
// when x/net/websocket's primitive rejects the handshake before hijacking.
type publicUpgradeWriter struct {
	http.ResponseWriter
	hijacked bool
	header   http.Header
}

func (w *publicUpgradeWriter) Header() http.Header { return w.header }

func (w *publicUpgradeWriter) WriteHeader(int)                {}
func (w *publicUpgradeWriter) Write(data []byte) (int, error) { return len(data), nil }
func (w *publicUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	host, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("websocket transport requires hijacking")
	}
	conn, rw, err := host.Hijack()
	w.hijacked = err == nil
	return conn, rw, err
}

func (socket *connection) CloseRemote(code uint16, reason string) {
	// A peer that stopped reading must not hold the device-store lock or
	// delay revocation/shutdown indefinitely.
	_ = socket.ws.SetWriteDeadline(time.Now().Add(time.Second))
	socket.closeWithReason(code, reason)
}
