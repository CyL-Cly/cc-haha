package publicaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

type Socket interface{ CloseRemote(uint16, string) }
type Dependencies struct {
	Authorization   access.Authorization
	StorePath       string
	Now             func() int64
	BusinessAllowed func([]string, string) bool
	Business        http.Handler
	Static          http.Handler
	Websocket       func(http.ResponseWriter, *http.Request, string) bool
}
type pending struct {
	id                string
	name              json.RawMessage
	expiresAt         int64
	claimHash, status string
}
type rate struct {
	start int64
	count int
}
type pairing struct {
	hash      string
	expiresAt int64
}
type Server struct {
	mu           sync.Mutex
	deps         Dependencies
	stored       Stored
	loaded       bool
	listener     net.Listener
	host         *http.Server
	publicURL    string
	pairing      *pairing
	pending      map[string]*pending
	pendingOrder []string
	rates        map[string]*rate
	sockets      map[Socket]string
	timerCancel  context.CancelFunc
	timerDone    chan struct{}
}

func New(deps Dependencies) *Server {
	if deps.Now == nil {
		deps.Now = func() int64 { return time.Now().UnixMilli() }
	}
	return &Server{deps: deps, stored: Stored{protocol.NewJSONObject(), []protocol.JSONObject{}}, pending: map[string]*pending{}, rates: map[string]*rate{}, sockets: map[Socket]string{}}
}
func parts(path string) []string {
	var out []string
	for _, part := range strings.Split(path, "/") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizedRequest(r *http.Request) (*http.Request, error) {
	parsed, err := weburl.Parse("http://127.0.0.1" + r.URL.RequestURI())
	if err != nil {
		return nil, err
	}
	decoded, err := url.PathUnescape(parsed.Pathname())
	if err != nil {
		return nil, err
	}
	copy := r.Clone(r.Context())
	address := *r.URL
	address.Path = decoded
	address.RawPath = parsed.Pathname()
	address.RawQuery = strings.TrimPrefix(parsed.Search(), "?")
	copy.URL = &address
	return copy, nil
}
func IsControlPath(path string) bool {
	p := parts(path)
	return len(p) >= 2 && p[0] == "api" && p[1] == "public-access"
}
func secure(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'; object-src 'none'; base-uri 'self'")
}
func writeJSON(w http.ResponseWriter, code int, value any) {
	secure(w.Header())
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(code)
	data, e := json.Marshal(value)
	if e == nil {
		data, e = protocol.ParseAndStringifyJavaScriptJSON(data)
	}
	if e == nil {
		_, _ = w.Write(data)
	}
}
func failure(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
func readInput(r *http.Request) (protocol.JSONObject, error) {
	data, e := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024+1))
	if e != nil || len(data) > 16*1024*1024 {
		return protocol.JSONObject{}, errors.New("Request too large")
	}
	// Preserve Bun's request.text BOM behavior and its UTF-16 length boundary.
	data = protocol.DecodeJavaScriptUTF8(data)
	if len(utf16.Encode([]rune(string(data)))) > 4096 {
		return protocol.JSONObject{}, errors.New("Request too large")
	}
	if len(data) == 0 {
		return protocol.NewJSONObject(), nil
	}
	var value protocol.JSONObject
	e = value.UnmarshalJSON(data)
	if e != nil || !value.IsObject() {
		return protocol.JSONObject{}, errors.New("Object required")
	}
	return value, nil
}
func (s *Server) loadLocked() error {
	if s.loaded {
		return nil
	}
	value, e := readStore(s.deps.StorePath)
	if e != nil {
		return e
	}
	s.stored = value
	if e = persistStore(s.deps.StorePath, s.stored); e != nil {
		return e
	}
	s.loaded = true
	return nil
}
func (s *Server) authorizedLocked(id string) bool {
	if s.listener == nil {
		return false
	}
	for _, d := range s.stored.Devices {
		n, _ := number(d.Get("expiresAt"))
		if identity(d.Get("id")) == id && n > float64(s.deps.Now()) {
			return true
		}
	}
	return false
}
func (s *Server) pruneLocked() {
	now := s.deps.Now()
	for key, r := range s.rates {
		if now-r.start >= 60000 {
			delete(s.rates, key)
		}
	}
	for id, p := range s.pending {
		if p.expiresAt <= now {
			delete(s.pending, id)
		}
	}
	order := s.pendingOrder[:0]
	for _, id := range s.pendingOrder {
		if s.pending[id] != nil {
			order = append(order, id)
		}
	}
	s.pendingOrder = order
	devices := s.stored.Devices[:0]
	for _, d := range s.stored.Devices {
		n, _ := number(d.Get("expiresAt"))
		if n > float64(now) {
			devices = append(devices, d)
		}
	}
	s.stored.Devices = devices
	for socket, id := range s.sockets {
		if !s.authorizedLocked(id) {
			socket.CloseRemote(1008, "Remote access revoked")
		}
	}
}
func (s *Server) statusLocked() map[string]any {
	s.pruneLocked()
	var port any
	var publicURL any
	if s.listener != nil {
		port = s.listener.Addr().(*net.TCPAddr).Port
	}
	if s.publicURL != "" {
		publicURL = s.publicURL
	}
	pendingRows := []protocol.JSONObject{}
	for _, id := range s.pendingOrder {
		if p := s.pending[id]; p != nil && p.status == "pending" {
			row := protocol.NewJSONObject()
			row.Set("id", raw(id))
			row.Set("name", p.name)
			pendingRows = append(pendingRows, row)
		}
	}
	devices := []protocol.JSONObject{}
	for _, d := range s.stored.Devices {
		row := protocol.NewJSONObject()
		for _, key := range []string{"id", "name", "createdAt", "expiresAt"} {
			row.Set(key, d.Get(key))
		}
		devices = append(devices, row)
	}
	return map[string]any{"enabled": s.listener != nil, "port": port, "publicUrl": publicURL, "pending": pendingRows, "devices": devices}
}
func (s *Server) originLocked(value json.RawMessage) error {
	if !stringValue(value) {
		return errors.New("HTTPS origin required")
	}
	u, e := weburl.Parse(object(raw(map[string]json.RawMessage{"value": value})).String("value"))
	if e != nil {
		return e
	}
	if u.Scheme() != "https" || u.Username() != "" || u.Password() != "" || u.Pathname() != "/" || u.Search() != "" || u.Hash() != "" {
		return errors.New("HTTPS origin required")
	}
	origin := "https://" + u.Host()
	if s.publicURL != "" && s.publicURL != origin {
		clear(s.pending)
		s.pendingOrder = nil
		s.pairing = nil
		for socket := range s.sockets {
			socket.CloseRemote(1008, "Remote origin changed")
		}
	}
	s.publicURL = origin
	return nil
}
func (s *Server) enableLocked(value json.RawMessage) error {
	if stringValue(value) && string(value) != `""` {
		if e := s.originLocked(value); e != nil {
			return e
		}
	}
	if s.listener != nil {
		return nil
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return e
	}
	host := &http.Server{Handler: s, ReadHeaderTimeout: 30 * time.Second}
	s.listener = listener
	s.host = host
	ctx, cancel := context.WithCancel(context.Background())
	s.timerCancel = cancel
	s.timerDone = make(chan struct{})
	done := s.timerDone
	go func() { _ = host.Serve(listener) }()
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				s.pruneLocked()
				s.mu.Unlock()
			}
		}
	}()
	return nil
}

// Disable closes the independent listener and its sockets without cancelling
// an already admitted Agent turn in the shared local server.
func (s *Server) Disable() map[string]any {
	s.mu.Lock()
	listener, host, cancel, done := s.listener, s.host, s.timerCancel, s.timerDone
	s.listener = nil
	s.host = nil
	s.timerCancel = nil
	s.timerDone = nil
	s.publicURL = ""
	s.pairing = nil
	clear(s.pending)
	s.pendingOrder = nil
	clear(s.rates)
	for socket := range s.sockets {
		socket.CloseRemote(1008, "Remote access disabled")
	}
	status := s.statusLocked()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if host != nil {
		_ = host.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if done != nil {
		<-done
	}
	return status
}
func (s *Server) Control(w http.ResponseWriter, r *http.Request) {
	if !s.deps.Authorization.LocalAuthorized(r.Header, nil) {
		failure(w, 403, "Desktop credential required")
		return
	}
	var err error
	r, err = normalizedRequest(r)
	if err != nil {
		failure(w, 400, "Invalid remote access request or unavailable device store")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := func() { failure(w, 400, "Invalid remote access request or unavailable device store") }
	if e := s.loadLocked(); e != nil {
		fail()
		return
	}
	route := strings.Join(parts(r.URL.EscapedPath())[2:], "/")
	if r.Method == "GET" && (route == "" || route == "status") {
		writeJSON(w, 200, s.statusLocked())
		return
	}
	input := protocol.NewJSONObject()
	if r.Method != "GET" {
		var e error
		input, e = readInput(r)
		if e != nil {
			fail()
			return
		}
	}
	switch {
	case r.Method == "POST" && route == "enable":
		if e := s.enableLocked(input.Get("publicUrl")); e != nil {
			fail()
			return
		}
		writeJSON(w, 200, s.statusLocked())
	case r.Method == "PUT" && route == "origin":
		if e := s.originLocked(input.Get("publicUrl")); e != nil {
			fail()
			return
		}
		writeJSON(w, 200, s.statusLocked())
	case r.Method == "POST" && route == "disable":
		s.mu.Unlock()
		status := s.Disable()
		s.mu.Lock()
		writeJSON(w, 200, status)
	case r.Method == "POST" && route == "pairing":
		if s.listener == nil || s.publicURL == "" {
			failure(w, 409, "Remote access is not ready")
			return
		}
		token, e := secret()
		if e != nil {
			fail()
			return
		}
		s.pairing = &pairing{hash(token), s.deps.Now() + PairTTL}
		writeJSON(w, 200, map[string]any{"secret": token, "expiresAt": s.pairing.expiresAt})
	case r.Method == "POST" && route == "revoke":
		id := identity(input.Get("id"))
		if id == "" {
			id = `""`
		}
		devices := s.stored.Devices[:0]
		for _, d := range s.stored.Devices {
			if identity(d.Get("id")) != id {
				devices = append(devices, d)
			}
		}
		s.stored.Devices = devices
		if e := persistStore(s.deps.StorePath, s.stored); e != nil {
			fail()
			return
		}
		writeJSON(w, 200, s.statusLocked())
	case r.Method == "POST" && (route == "approve" || route == "reject"):
		s.pruneLocked()
		p := s.pending[input.String("id")]
		if p == nil || p.status != "pending" {
			failure(w, 404, "Pairing request expired")
			return
		}
		if route == "approve" {
			p.status = "approved"
		} else {
			p.status = "rejected"
		}
		writeJSON(w, 200, s.statusLocked())
	default:
		failure(w, 404, "Not found")
	}
}
func (s *Server) rateLocked(key string, limit int) bool {
	now := s.deps.Now()
	r := s.rates[key]
	if r == nil || now-r.start >= 60000 {
		s.rates[key] = &rate{now, 1}
		return true
	}
	r.count++
	return r.count <= limit
}
func (s *Server) deviceLocked(r *http.Request) string {
	var token string
	for _, part := range strings.Split(fetchHeader(r.Header, "Cookie"), ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, CookieName+"=") {
			token = strings.TrimPrefix(part, CookieName+"=")
			break
		}
	}
	if token == "" || len(token) > 128 || s.listener == nil {
		return ""
	}
	for _, d := range s.stored.Devices {
		expiry, _ := number(d.Get("expiresAt"))
		if d.String("tokenHash") == hash(token) && expiry > float64(s.deps.Now()) {
			return identity(d.Get("id"))
		}
	}
	return ""
}

func fetchHeader(header http.Header, key string) string {
	values := header.Values(key)
	return strings.Trim(strings.Join(values, ", "), " \t\r\n")
}

// Device IDs in old v1 stores may contain empty strings or lone UTF-16 units.
// Preserve their exact identity instead of conflating them with no device.
func identity(value json.RawMessage) string {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return ""
	}
	return string(protocol.QuoteJavaScriptUTF16(units))
}
func deviceName(input protocol.JSONObject) json.RawMessage {
	units, e := protocol.DecodeJavaScriptJSONString(input.Get("name"))
	if e != nil {
		return raw("Mobile browser")
	}
	clean := units[:0]
	for _, u := range units {
		if u > 31 && u != 127 {
			clean = append(clean, u)
		}
	}
	if len(clean) > 80 {
		clean = clean[:80]
	}
	return protocol.QuoteJavaScriptUTF16(clean)
}
func (s *Server) publicControlLocked(w http.ResponseWriter, r *http.Request, route string) {
	if !s.rateLocked("pairing-global", 180) {
		failure(w, 429, "Too many requests")
		return
	}
	if route == "api/public-access/session" && r.Method == "GET" {
		writeJSON(w, 200, map[string]bool{"authenticated": s.deviceLocked(r) != ""})
		return
	}
	input, e := readInput(r)
	if e != nil {
		failure(w, 400, "Remote request failed")
		return
	}
	s.pruneLocked()
	if route == "api/public-access/pair" && r.Method == "POST" {
		if !s.rateLocked("pair-attempts", 20) {
			failure(w, 429, "Too many attempts")
			return
		}
		if s.pairing == nil || s.pairing.expiresAt <= s.deps.Now() || !stringValue(input.Get("secret")) || hash(input.String("secret")) != s.pairing.hash {
			failure(w, 401, "Pairing code expired or invalid")
			return
		}
		if len(s.stored.Devices) >= 32 || len(s.pending) >= 8 {
			failure(w, 429, "Device limit reached")
			return
		}
		expires := s.pairing.expiresAt
		s.pairing = nil
		id, e := secret()
		if e != nil {
			failure(w, 400, "Remote request failed")
			return
		}
		claim, e := secret()
		if e != nil {
			failure(w, 400, "Remote request failed")
			return
		}
		s.pending[id] = &pending{id, deviceName(input), expires, hash(claim), "pending"}
		s.pendingOrder = append(s.pendingOrder, id)
		writeJSON(w, 200, map[string]string{"id": id, "claimSecret": claim})
		return
	}
	if route == "api/public-access/claim" && r.Method == "POST" {
		p := s.pending[input.String("id")]
		if p == nil || !stringValue(input.Get("claimSecret")) || hash(input.String("claimSecret")) != p.claimHash {
			failure(w, 401, "Pairing request expired or invalid")
			return
		}
		if p.status != "approved" {
			writeJSON(w, 200, map[string]string{"status": p.status})
			return
		}
		token, e := secret()
		if e != nil {
			failure(w, 400, "Remote request failed")
			return
		}
		device := protocol.NewJSONObject()
		device.Set("id", raw(p.id))
		device.Set("name", p.name)
		device.Set("createdAt", raw(s.deps.Now()))
		device.Set("expiresAt", raw(s.deps.Now()+SessionTTL))
		device.Set("tokenHash", raw(hash(token)))
		s.stored.Devices = append(s.stored.Devices, device)
		if e = persistStore(s.deps.StorePath, s.stored); e != nil {
			s.stored.Devices = s.stored.Devices[:len(s.stored.Devices)-1]
			failure(w, 400, "Remote request failed")
			return
		}
		delete(s.pending, p.id)
		w.Header().Set("Set-Cookie", CookieName+"="+token+"; Path=/; Secure; HttpOnly; SameSite=Strict; Max-Age="+strconv.Itoa(SessionTTL/1000))
		writeJSON(w, 200, map[string]string{"status": "approved"})
		return
	}
	failure(w, 404, "Not found")
}

// RegisterSocket and CheckMessage wrap the actual local websocket consumer;
// the public listener never creates a second Agent runtime or credential.
func (s *Server) RegisterSocket(id string, socket Socket) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, device := range s.sockets {
		if device == id {
			count++
		}
	}
	if !s.authorizedLocked(id) || len(s.sockets) >= 64 || count >= 8 {
		socket.CloseRemote(1008, "Remote connection limit")
		return false
	}
	s.sockets[socket] = id
	return true
}
func (s *Server) UnregisterSocket(socket Socket) {
	s.mu.Lock()
	delete(s.sockets, socket)
	s.mu.Unlock()
}
func (s *Server) CheckMessage(id string, socket Socket) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authorizedLocked(id) {
		socket.CloseRemote(1008, "Remote access revoked")
		return false
	}
	if !s.rateLocked("ws:"+id, 600) {
		socket.CloseRemote(1008, "Remote rate limit")
		return false
	}
	return true
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var err error
	r, err = normalizedRequest(r)
	if err != nil {
		failure(w, 400, "Remote request failed")
		return
	}
	s.mu.Lock()
	if s.listener == nil || s.publicURL == "" {
		s.mu.Unlock()
		failure(w, 503, "Remote access unavailable")
		return
	}
	p := parts(r.URL.EscapedPath())
	root := ""
	if len(p) > 0 {
		root = p[0]
	}
	origin := fetchHeader(r.Header, "Origin")
	site := fetchHeader(r.Header, "Sec-Fetch-Site")
	if origin != "" && origin != s.publicURL || (root == "api" || root == "ws") && (site == "cross-site" || site == "same-site") {
		s.mu.Unlock()
		failure(w, 403, "Origin rejected")
		return
	}
	if (r.Method != "GET" && r.Method != "HEAD" || strings.HasPrefix(r.URL.EscapedPath(), "/ws/")) && origin != s.publicURL {
		s.mu.Unlock()
		failure(w, 403, "Origin required")
		return
	}
	if r.URL.EscapedPath() == "/health" && r.Method == "GET" {
		s.mu.Unlock()
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	}
	route := strings.Join(p, "/")
	if strings.HasPrefix(route, "api/public-access/") {
		s.publicControlLocked(w, r, route)
		s.mu.Unlock()
		return
	}
	protected := false
	for _, value := range []string{"api", "ws", "sdk", "proxy", "local-file", "preview-fs"} {
		if root == value {
			protected = true
		}
	}
	if protected {
		device := s.deviceLocked(r)
		if device == "" {
			s.mu.Unlock()
			failure(w, 401, "Device pairing required")
			return
		}
		if s.deps.BusinessAllowed == nil || !s.deps.BusinessAllowed(p, r.Method) {
			s.mu.Unlock()
			failure(w, 403, "Desktop-only capability")
			return
		}
		if !s.rateLocked("api:"+device, 1200) {
			s.mu.Unlock()
			failure(w, 429, "Too many requests")
			return
		}
		s.mu.Unlock()
		if strings.HasPrefix(r.URL.EscapedPath(), "/ws/") {
			if s.deps.Websocket == nil {
				failure(w, 400, "Upgrade failed")
				return
			}
			if !s.deps.Websocket(w, r, device) {
				failure(w, 400, "Upgrade failed")
			}
			return
		}
		if s.deps.Business == nil {
			failure(w, 400, "Remote request failed")
			return
		}
		projection := route == "api/providers/auth-status" || route == "api/settings/user" && r.Method == "GET"
		if !projection {
			s.deps.Business.ServeHTTP(&securedWriter{ResponseWriter: w}, r)
			return
		}
		response := &bufferedResponse{header: http.Header{}}
		s.deps.Business.ServeHTTP(response, r)
		if response.status >= 200 && response.status < 300 && (route == "api/providers/auth-status" || route == "api/settings/user" && r.Method == "GET") {
			var value protocol.JSONObject
			if e := value.UnmarshalJSON(response.body.Bytes()); e != nil || !value.IsObject() {
				failure(w, 400, "Remote request failed")
				return
			}
			result := protocol.NewJSONObject()
			if route == "api/providers/auth-status" {
				result.Set("hasAuth", raw(string(value.Get("hasAuth")) == "true"))
				source := value.String("source")
				allowed := false
				for _, v := range []string{"cc-haha-provider", "claude-oauth", "openai-oauth", "grok-oauth", "original-settings", "env", "none"} {
					if source == v {
						allowed = true
					}
				}
				if !allowed {
					source = "none"
				}
				result.Set("source", raw(source))
				if stringValue(value.Get("activeProvider")) {
					result.Set("activeProvider", value.Get("activeProvider"))
				}
			} else {
				for _, key := range []string{"alwaysThinkingEnabled", "workflowKeywordTriggerEnabled", "autoDreamEnabled", "skipAutoPermissionPrompt", "chatSendBehavior", "outputStyle", "skipWebFetchPreflight", "language"} {
					v := value.Get(key)
					_, n := number(v)
					if stringValue(v) || string(v) == "true" || string(v) == "false" || n {
						result.Set(key, v)
					}
				}
			}
			writeJSON(w, 200, result)
			return
		}
		for key, values := range response.header {
			w.Header()[key] = values
		}
		secure(w.Header())
		if response.status == 0 {
			response.status = 200
		}
		w.WriteHeader(response.status)
		_, _ = w.Write(response.body.Bytes())
		return
	}
	s.mu.Unlock()
	secure(w.Header())
	if r.URL.EscapedPath() == "/" {
		w.Header().Set("Location", "/remote")
		w.WriteHeader(302)
		return
	}
	if s.deps.Static != nil {
		s.deps.Static.ServeHTTP(&securedWriter{ResponseWriter: w}, r)
	} else {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, "Not found")
	}
}

// Decorate at the commit boundary so static cache headers cannot overwrite
// the original remote no-store policy, and ordinary bodies keep streaming.
type securedWriter struct {
	http.ResponseWriter
	committed bool
}

func (w *securedWriter) WriteHeader(code int) {
	if !w.committed {
		secure(w.Header())
		w.committed = true
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *securedWriter) Write(data []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(data)
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *bufferedResponse) Header() http.Header { return r.header }
func (r *bufferedResponse) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *bufferedResponse) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	return r.body.Write(data)
}
