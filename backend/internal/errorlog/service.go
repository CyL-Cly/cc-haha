// Package errorlog owns the Source logError memory, privacy and sink queue.
// It is separate from analytics and from the desktop diagnostics format.
package errorlog

import (
	"errors"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
)

type Error struct{ Name, Message, Stack string }

func (e Error) Error() string { return e.Message }

type Row struct {
	Error     string `json:"error"`
	Timestamp string `json:"timestamp"`
}
type Sink func(error)
type Options struct {
	Environment func() map[string]string
	Now         func() time.Time
}
type Service struct {
	mu      sync.Mutex
	options Options
	memory  []Row
	queue   []error
	sink    Sink
}

func New(o Options) (*Service, error) {
	if o.Environment == nil || o.Now == nil {
		return nil, errors.New("error logging requires actual environment and clock")
	}
	return &Service{options: o, memory: []Row{}}, nil
}
func Disabled(env map[string]string) bool {
	if env["DISABLE_ERROR_REPORTING"] != "" || env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "" {
		return true
	}
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if analytics.EnvTruthy(env[key]) {
			return true
		}
	}
	return false
}
func Details(err error) Error {
	var explicit Error
	if errors.As(err, &explicit) {
		return explicit
	}
	var pointer *Error
	if errors.As(err, &pointer) && pointer != nil {
		return *pointer
	}
	if err == nil {
		return Error{Name: "Error", Message: "undefined"}
	}
	return Error{Name: "Error", Message: err.Error()}
}
func Text(err error) string {
	e := Details(err)
	if e.Stack != "" {
		return e.Stack
	}
	return e.Message
}
func timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// Source logError catches errors from the reporting backend. Error reporting
// can never turn a recoverable credential lookup into a failed user operation.
func (s *Service) Log(err error) {
	defer func() { _ = recover() }()
	if Disabled(s.options.Environment()) {
		return
	}
	row := Row{Error: Text(err), Timestamp: timestamp(s.options.Now())}
	s.mu.Lock()
	if len(s.memory) >= 100 {
		s.memory = s.memory[1:]
	}
	s.memory = append(s.memory, row)
	sink := s.sink
	if sink == nil {
		s.queue = append(s.queue, err)
	}
	s.mu.Unlock()
	if sink != nil {
		sink(err)
	}
}

// Attach retains the first sink identity and drains pre-attachment errors in
// insertion order, using the original error values rather than memory rows.
func (s *Service) Attach(sink Sink) {
	if sink == nil {
		return
	}
	s.mu.Lock()
	if s.sink != nil {
		s.mu.Unlock()
		return
	}
	s.sink = sink
	queue := s.queue
	s.queue = nil
	s.mu.Unlock()
	for _, err := range queue {
		sink(err)
	}
}
func (s *Service) Memory() []Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Row{}, s.memory...)
}
