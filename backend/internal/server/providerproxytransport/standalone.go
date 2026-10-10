package providerproxytransport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Standalone is the CLI process's single loopback proxy, created lazily by the
// actual managed-env producer. A desktop-owned provider uses its host instead.
type Standalone struct {
	mu       sync.Mutex
	service  *Service
	server   *http.Server
	port     int
	done     chan struct{}
	cancel   context.CancelFunc
	closed   bool
	requests sync.WaitGroup
}

func NewStandalone(options Options) (*Standalone, error) {
	service, err := New(options)
	if err != nil {
		return nil, err
	}
	return &Standalone{service: service}, nil
}
func (s *Standalone) EnsurePort() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("Standalone provider proxy is closed")
	}
	if s.server != nil {
		return s.port, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.port = listener.Addr().(*net.TCPAddr).Port
	s.done = make(chan struct{})
	s.server = &http.Server{BaseContext: func(net.Listener) context.Context { return ctx }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.requests.Add(1)
		s.mu.Unlock()
		defer s.requests.Done()
		if r.URL.Path == "/health" {
			response := jsonResponse(200, record(field("status", "ok")))
			defer response.Body.Close()
			w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/proxy/") {
			if err := s.service.Serve(w, r); err != nil {
				http.Error(w, "Internal Server Error", 500)
			}
			return
		}
		response := jsonResponse(404, record(field("error", "Not Found")))
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(404)
		_, _ = io.Copy(w, response.Body)
	})}
	server, done := s.server, s.done
	go func() { defer close(done); _ = server.Serve(listener) }()
	return s.port, nil
}
func (s *Standalone) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	server, done, cancel := s.server, s.done, s.cancel
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	cancel()
	err := server.Close()
	joined := make(chan struct{})
	go func() { s.requests.Wait(); close(joined) }()
	select {
	case <-done:
		select {
		case <-joined:
			return err
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
