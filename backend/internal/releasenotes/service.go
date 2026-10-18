package releasenotes

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type FetchResponse struct {
	Status        int
	Content       string
	StringContent bool
}
type Options struct {
	CachePath     string
	ReadFile      func(string) ([]byte, error)
	MkdirAll      func(string) error
	WriteFile     func(string, []byte) error
	Fetch         func(context.Context) (FetchResponse, error)
	SaveTimestamp func(float64) error
	NowMillis     func() float64
}
type Service struct {
	o      Options
	mu     sync.Mutex
	memory *string
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(o Options) (*Service, error) {
	if o.CachePath == "" || o.ReadFile == nil || o.MkdirAll == nil || o.WriteFile == nil || o.Fetch == nil || o.SaveTimestamp == nil || o.NowMillis == nil {
		return nil, errors.New("release notes requires actual cache, fetch and config producers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, ctx: ctx, cancel: cancel}, nil
}
func (s *Service) Stored() string {
	s.mu.Lock()
	if s.memory != nil {
		value := *s.memory
		s.mu.Unlock()
		return value
	}
	s.mu.Unlock()
	data, err := s.o.ReadFile(s.o.CachePath)
	value := ""
	if err == nil {
		value = string(protocol.DecodeJavaScriptUTF8(data))
	}
	s.mu.Lock()
	s.memory = &value
	s.mu.Unlock()
	return value
}
func (s *Service) Memory() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.memory == nil {
		return ""
	}
	return *s.memory
}

// Preload is setup's awaited cached read followed by its independent refresh.
// Logo renderers read Memory; they do not reread a file changed after startup.
func (s *Service) Preload(nonInteractive, essentialOnly, versionChanged bool) string {
	content := s.Stored()
	s.mu.Lock()
	if !s.closed && !nonInteractive && !essentialOnly && (versionChanged || content == "") {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); _ = s.fetch() }()
	}
	s.mu.Unlock()
	return content
}
func (s *Service) fetch() error {
	response, err := s.o.Fetch(s.ctx)
	if err != nil {
		return err
	}
	if response.Status != 200 {
		return nil
	}
	if !response.StringContent {
		return errors.New("changelog write requires a string response")
	}
	s.mu.Lock()
	unchanged := s.memory != nil && *s.memory == response.Content
	s.mu.Unlock()
	if unchanged {
		return nil
	}
	if err = s.o.MkdirAll(filepath.Dir(s.o.CachePath)); err != nil {
		return err
	}
	if err = s.o.WriteFile(s.o.CachePath, []byte(response.Content)); err != nil {
		return err
	}
	value := response.Content
	s.mu.Lock()
	s.memory = &value
	s.mu.Unlock()
	return s.o.SaveTimestamp(s.o.NowMillis())
}

// Command preserves Promise.race's 500ms wait. A timed-out download retains
// its own lifetime, and its successful disk/config changes remain observable.
func (s *Service) Command(ctx context.Context, nonInteractive, essentialOnly bool) (string, error) {
	done := make(chan error, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", context.Canceled
	}
	if nonInteractive || essentialOnly {
		done <- nil
	} else {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); done <- s.fetch() }()
	}
	s.mu.Unlock()
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	var fresh []Entry
	select {
	case err := <-done:
		if err == nil {
			fresh = All(s.Stored())
		}
	case <-timer.C:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if len(fresh) > 0 {
		return Format(fresh), nil
	}
	if cached := All(s.Stored()); len(cached) > 0 {
		return Format(cached), nil
	}
	return "See the full changelog at: " + ChangelogURL, nil
}

// Close is the native process-lifetime adapter. Only process shutdown cancels
// pending HTTP; finishing a command or reaching the 500ms race never does.
func (s *Service) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock(); s.wg.Wait() }
