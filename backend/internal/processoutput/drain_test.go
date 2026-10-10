package processoutput

import (
	"errors"
	"sync"
	"syscall"
	"testing"
	"time"
)

type controlledStream struct {
	mu                  sync.Mutex
	callbacks           []func(error)
	drain               func()
	destroyed           bool
	accepted            bool
	synchronousCallback bool
	writeError          error
}

func (s *controlledStream) adapter() *Stream {
	return &Stream{
		Write: func(_ []byte, callback func(error)) (bool, error) {
			s.mu.Lock()
			s.callbacks = append(s.callbacks, callback)
			s.mu.Unlock()
			if s.synchronousCallback {
				callback(nil)
			}
			return s.accepted, s.writeError
		},
		Destroyed: func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.destroyed },
		Destroy:   func() { s.mu.Lock(); defer s.mu.Unlock(); s.destroyed = true },
		OnceDrain: func(callback func()) { s.mu.Lock(); defer s.mu.Unlock(); s.drain = callback },
	}
}

func (s *controlledStream) complete(index int, err error) {
	s.mu.Lock()
	cb := s.callbacks[index]
	s.mu.Unlock()
	cb(err)
}
func (s *controlledStream) drained() {
	s.mu.Lock()
	cb := s.drain
	s.drain = nil
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func TestCallbackAndDrainBothRequired(t *testing.T) {
	for _, order := range []string{"callback-first", "drain-first", "callback-before-write-return"} {
		t.Run(order, func(t *testing.T) {
			m := New()
			s := &controlledStream{synchronousCallback: order == "callback-before-write-return"}
			stream := s.adapter()
			if err := m.Write(stream, []byte("original-output\n")); err != nil {
				t.Fatal(err)
			}
			if m.Budget() != 2001*time.Millisecond {
				t.Fatalf("budget=%v", m.Budget())
			}
			switch order {
			case "callback-first":
				s.complete(0, nil)
			case "drain-first":
				s.drained()
			}
			if m.Budget() == 0 {
				t.Fatal("one acknowledgement falsely settled output")
			}
			if order == "drain-first" {
				s.complete(0, nil)
			} else {
				s.drained()
			}
			m.Flush(m.Budget())
			if m.Budget() != 0 {
				t.Fatal("both signals left pending output")
			}
		})
	}
}

func TestErrorIdentityAndBrokenPipeScope(t *testing.T) {
	for _, synchronous := range []bool{false, true} {
		for _, broken := range []bool{false, true} {
			t.Run(map[bool]string{true: "sync", false: "callback"}[synchronous]+map[bool]string{true: "-EPIPE", false: "-EIO"}[broken], func(t *testing.T) {
				m := New()
				stdout, stderr := &controlledStream{}, &controlledStream{}
				out, other := stdout.adapter(), stderr.adapter()
				cause := error(syscall.EIO)
				if broken {
					cause = syscall.EPIPE
				}
				original := &codedError{cause: cause}
				if err := m.Write(out, []byte("queued")); err != nil {
					t.Fatal(err)
				}
				if err := m.Write(other, []byte("unrelated")); err != nil {
					t.Fatal(err)
				}
				if synchronous {
					stdout.writeError = original
				}
				err := m.Write(out, []byte("tail"))
				if !synchronous {
					stdout.complete(1, original)
				}
				if synchronous && !broken {
					if err != original {
						t.Fatalf("original synchronous error lost: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if stdout.destroyed != broken || stderr.destroyed {
					t.Fatal("incorrect stream destruction")
				}
				if broken {
					if m.Budget() != 2001*time.Millisecond {
						t.Fatal("broken pipe settled unrelated stderr")
					}
					if err := m.Write(out, []byte("ignored")); err != nil {
						t.Fatal(err)
					}
					if len(stdout.callbacks) != 2 {
						t.Fatal("destroyed stream written again")
					}
				} else {
					stdout.complete(0, nil)
					stdout.drained()
				}
				stderr.complete(0, nil)
				stderr.drained()
				m.Flush(m.Budget())
				if m.Budget() != 0 {
					t.Fatal("pending writes remain")
				}
			})
		}
	}
}

type codedError struct{ cause error }

func (e *codedError) Error() string { return "original native output error" }
func (e *codedError) Unwrap() error { return e.cause }

func TestDrainBudgetUtf8CapAndDeadlineRetainsPending(t *testing.T) {
	m := New()
	s := &controlledStream{}
	stream := s.adapter()
	if m.Budget() != 0 {
		t.Fatal("empty output has budget")
	}
	m.Flush(0)
	if err := m.Write(stream, []byte("中🙂")); err != nil {
		t.Fatal(err)
	}
	if m.Budget() != 2001*time.Millisecond {
		t.Fatal("UTF8 bytes budget")
	}
	if err := m.Write(stream, make([]byte, 4*1024*1024)); err != nil {
		t.Fatal(err)
	}
	if m.Budget() != 10001*time.Millisecond {
		t.Fatalf("budget=%v", m.Budget())
	}
	if err := m.Write(stream, make([]byte, 16*1024*1024)); err != nil {
		t.Fatal(err)
	}
	if m.Budget() != 30*time.Second {
		t.Fatal("uncapped budget")
	}
	m.Flush(time.Nanosecond)
	if m.Budget() == 0 {
		t.Fatal("deadline discarded output obligations")
	}
	for i := 0; i < 3; i++ {
		s.complete(i, nil)
	}
	s.drained()
	m.Flush(m.Budget())
	if m.Budget() != 0 {
		t.Fatal("post deadline delivery did not settle")
	}
}

func TestAcceptedSynchronousCallbackAndDoubleCompletion(t *testing.T) {
	m := New()
	s := &controlledStream{accepted: true, synchronousCallback: true}
	stream := s.adapter()
	if err := m.Write(stream, []byte("a")); err != nil {
		t.Fatal(err)
	}
	s.complete(0, nil)
	if m.Budget() != 0 {
		t.Fatal("accepted callback falsely pending")
	}
	m.Flush(time.Second)
}

func TestOutputEventBrokenPipeAndConcurrentFlush(t *testing.T) {
	m := New()
	s := &controlledStream{}
	stream := s.adapter()
	if err := m.Write(stream, []byte("a")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.Flush(time.Second); close(done) }()
	m.HandleError(stream, errors.New("non-EPIPE"))
	if m.Budget() == 0 {
		t.Fatal("ordinary output event settled writes")
	}
	m.HandleError(stream, &codedError{cause: syscall.EPIPE})
	<-done
	if m.Budget() != 0 || !stream.Destroyed() {
		t.Fatal("EPIPE event failed settlement")
	}
	s.complete(0, nil)
	s.drained()
	if m.Budget() != 0 {
		t.Fatal("late native acknowledgements restored pending output")
	}
}

func TestOutputAdmissionRetainsImmutableBytes(t *testing.T) {
	m := New()
	var retained []byte
	var complete func(error)
	stream := &Stream{Destroyed: func() bool { return false }, Destroy: func() {}, OnceDrain: func(func()) {}, Write: func(data []byte, callback func(error)) (bool, error) {
		retained, complete = data, callback
		return true, nil
	}}
	data := []byte("original-output\n")
	if err := m.Write(stream, data); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	if string(retained) != "original-output\n" {
		t.Fatalf("caller changed already admitted output: %q", retained)
	}
	complete(nil)
	m.Flush(m.Budget())
}

func TestOutputZeroByteWriteDoesNotDelayInitialFlush(t *testing.T) {
	m := New()
	s := &controlledStream{}
	stream := s.adapter()
	if err := m.Write(stream, nil); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() { m.Flush(time.Hour); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("zero-byte output delayed initial flush")
	}
	s.complete(0, nil)
	s.drained()
}
