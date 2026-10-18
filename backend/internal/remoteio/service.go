package remoteio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

type Options struct {
	URL, SessionID, UserAgent string
	Environment               map[string]string
	IngressAuth               *IngressAuth
	HTTP                      *http.Client
	WebSocketHTTP             func(map[string]string) (*http.Client, error)
	// GlobalHTTP follows Source's reconfigured global Axios/fetch clients.
	// CCR's separately constructed worker client continues to use HTTP.
	GlobalHTTP      func(map[string]string) (*http.Client, error)
	InitialInput    io.Reader
	BridgeOutput    io.Writer
	Debug           bool
	BridgeKeepAlive *time.Duration
	// OnClose reports the terminal transport close, never transient failures.
	// The callback must not join its own transport worker.
	OnClose      func(code int, err error)
	OnConnect    func()
	OnDiagnostic func(string)
	// Native timing primitives are explicit so deterministic Source fixtures
	// can compare retry/liveness decisions without waiting ten minutes.
	Now    func() time.Time
	Wait   func(context.Context, time.Duration) error
	Random func() float64
}

type Service struct {
	options                   Options
	endpoints                 Endpoints
	ctx                       context.Context
	cancel                    context.CancelFunc
	mu                        sync.Mutex
	writeMu                   sync.Mutex
	wsWriteMu                 sync.Mutex
	wg                        sync.WaitGroup
	closed, started, finished bool
	closeDone                 chan struct{}
	env                       map[string]string
	input                     *inputStream
	conn                      *websocket.Conn
	buffer                    []protocol.JSONObject
	lastSentID                string
	terminalErr               error
	terminalCode              int
	sequence                  int
	seen                      map[int]bool
	ccr                       *ccrClient
	hybrid                    *batchUploader
	streamBuffer              []protocol.JSONObject
	streamTimer               *time.Timer
}

func New(ctx context.Context, options Options) (*Service, error) {
	endpoints, err := SelectEndpoints(options.URL, options.Environment)
	if err != nil {
		return nil, err
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Float64
	}
	if options.Wait == nil {
		options.Wait = wait
	}
	if options.HTTP == nil {
		options.HTTP = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	if options.IngressAuth == nil {
		options.IngressAuth = NewIngressAuth(options.Environment)
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{options: options, endpoints: endpoints, ctx: ctx, cancel: cancel, closeDone: make(chan struct{}), env: map[string]string{}, input: newInputStream(), seen: map[int]bool{}}
	for key, value := range options.Environment {
		s.env[key] = value
	}
	if endpoints.Kind == Hybrid {
		s.hybrid = newBatchUploader(s, 500, 100000, 0, 8000*time.Millisecond, 1000*time.Millisecond, s.postHybrid)
		// Hybrid.close retires the socket immediately while giving already
		// admitted POSTs a separate, bounded three-second grace period.
		s.hybrid.ctx, s.hybrid.cancel = context.WithCancel(context.WithoutCancel(ctx))
	}
	if endpoints.Kind == CCR {
		// The received-delivery consumer exists before Start admits any SSE
		// frame, including catch-up events that race worker initialization.
		s.ccr = newCCRClient(s)
	}
	return s, nil
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

func (s *Service) spawn(work func()) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() { defer s.wg.Done(); work() }()
	return true
}

func (s *Service) Start() {
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	// The owned lifetime also ends a blocked reader. A transport loop may
	// return on cancellation without reaching a protocol permanent-close frame.
	s.spawn(func() {
		<-s.ctx.Done()
		_ = s.input.Close()
	})
	if s.ccr != nil {
		s.spawn(func() { s.ccr.initialize() })
	}
	if s.endpoints.Kind == CCR {
		s.spawn(s.sseLoop)
	} else {
		s.spawn(s.webSocketLoop)
	}
	if s.options.InitialInput != nil {
		s.spawn(s.copyInitialInput)
	}
	if s.environment("CLAUDE_CODE_ENVIRONMENT_KIND") == "bridge" {
		interval := 120 * time.Second
		if s.options.BridgeKeepAlive != nil {
			interval = *s.options.BridgeKeepAlive
		}
		if interval <= 0 {
			return
		}
		s.spawn(func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
					_ = s.WriteMessage(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("keep_alive")}))
				}
			}
		})
	}
}

func (s *Service) copyInitialInput() {
	reader := bufio.NewReader(s.options.InitialInput)
	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			chunk = append(bytes.TrimSuffix(chunk, []byte{'\n'}), '\n')
			if _, failure := s.input.Write(chunk); failure != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Service) Read(data []byte) (int, error) { return s.input.Read(data) }
func (s *Service) Kind() Kind                    { return s.endpoints.Kind }

func (s *Service) Write(data []byte) (int, error) {
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var message protocol.JSONObject
		if err := message.UnmarshalJSON(line); err != nil {
			return 0, err
		}
		if err := s.WriteMessage(message); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (s *Service) WriteMessage(message protocol.JSONObject) error {
	var err error
	switch s.endpoints.Kind {
	case CCR:
		err = s.ccr.writeEvent(message)
	case Hybrid:
		err = s.writeHybrid(message)
	default:
		err = s.writeWebSocket(message)
	}
	if err == nil && s.environment("CLAUDE_CODE_ENVIRONMENT_KIND") == "bridge" && (message.String("type") == "control_request" || s.options.Debug) && s.options.BridgeOutput != nil {
		data, e := protocol.EncodeNDJSON(message)
		if e != nil {
			return e
		}
		s.writeMu.Lock()
		_, err = s.options.BridgeOutput.Write(data)
		s.writeMu.Unlock()
	}
	return err
}

func (s *Service) receive(data []byte) {
	_, _ = s.input.Write(data)
	if s.environment("CLAUDE_CODE_ENVIRONMENT_KIND") == "bridge" && s.options.Debug && s.options.BridgeOutput != nil {
		line := append([]byte(nil), data...)
		if !bytes.HasSuffix(line, []byte{'\n'}) {
			line = append(line, '\n')
		}
		s.writeMu.Lock()
		_, _ = s.options.BridgeOutput.Write(line)
		s.writeMu.Unlock()
	}
}

func (s *Service) finish(code int, err error) {
	s.mu.Lock()
	if s.closed || s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	s.terminalCode, s.terminalErr = code, err
	s.mu.Unlock()
	_ = s.input.Close()
	if s.options.OnClose != nil {
		s.options.OnClose(code, err)
	}
}

func (s *Service) Err() error     { s.mu.Lock(); defer s.mu.Unlock(); return s.terminalErr }
func (s *Service) CloseCode() int { s.mu.Lock(); defer s.mu.Unlock(); return s.terminalCode }

func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		return nil
	}
	s.closed = true
	conn := s.conn
	s.conn = nil
	if s.streamTimer != nil {
		s.streamTimer.Stop()
		s.streamTimer = nil
	}
	s.streamBuffer = nil
	s.mu.Unlock()
	s.cancel()
	_ = s.input.Close()
	if conn != nil {
		_ = conn.CloseNow()
	}
	if closer, ok := s.options.InitialInput.(io.ReadCloser); ok {
		_ = closer.Close()
	}
	if s.ccr != nil {
		s.ccr.close()
	}
	if s.hybrid != nil {
		grace, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.hybrid.flush(grace)
		cancel()
		s.hybrid.close()
	}
	s.wg.Wait()
	if transport, ok := s.options.HTTP.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
	close(s.closeDone)
	return nil
}

func (s *Service) diagnostic(event string) {
	if s.options.OnDiagnostic != nil {
		s.options.OnDiagnostic(event)
	}
}
func (s *Service) retryDelay(attempt int, maximum time.Duration, jitter float64) time.Duration {
	base := math.Min(1000*math.Pow(2, float64(attempt-1)), float64(maximum/time.Millisecond))
	return time.Duration(math.Max(0, base+base*jitter*(2*s.options.Random()-1))) * time.Millisecond
}
func quote(value string) json.RawMessage { data, _ := json.Marshal(value); return data }
func raw(value any) json.RawMessage      { data, _ := json.Marshal(value); return data }
func object(value json.RawMessage) protocol.JSONObject {
	var result protocol.JSONObject
	_ = result.UnmarshalJSON(value)
	return result
}
func (s *Service) flush(ctx context.Context) error {
	if s.ccr != nil {
		return s.ccr.flush(ctx)
	}
	if s.hybrid != nil {
		s.flushHybridStream()
		return s.hybrid.flush(ctx)
	}
	return nil
}
func (s *Service) Flush(ctx context.Context) error { return s.flush(ctx) }

var errEpochMismatch = errors.New("CCRClient: Epoch mismatch (409)")
