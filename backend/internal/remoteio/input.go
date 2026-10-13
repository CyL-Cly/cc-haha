package remoteio

import (
	"bytes"
	"io"
	"sync"
)

// inputStream is the original PassThrough's queued UTF-8 stream. A temporary
// network disconnection never ends it; only an intentional/permanent close
// ends the stream after already received bytes have been consumed.
type inputStream struct {
	mu    sync.Mutex
	ready *sync.Cond
	data  bytes.Buffer
	ended bool
}

func newInputStream() *inputStream {
	s := &inputStream{}
	s.ready = sync.NewCond(&s.mu)
	return s
}
func (s *inputStream) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return 0, io.ErrClosedPipe
	}
	n, err := s.data.Write(data)
	s.ready.Broadcast()
	return n, err
}
func (s *inputStream) Read(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.data.Len() == 0 && !s.ended {
		s.ready.Wait()
	}
	if s.data.Len() == 0 {
		return 0, io.EOF
	}
	return s.data.Read(data)
}
func (s *inputStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	s.ready.Broadcast()
	return nil
}
