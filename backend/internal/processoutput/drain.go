// Package processoutput retains the CLI process-output acknowledgement contract.
// Admission does not wait for physical output or resume an Agent iterator.
// Native transports supply callback/drain events; this package owns only queued
// byte accounting and the bounded final process-output flush.
package processoutput

import (
	"errors"
	"math"
	"sync"
	"syscall"
	"time"
)

// Stream keeps native object identity, including the distinction between stdout
// and stderr. Write returns admission plus the original synchronous failure;
// callback failures are later acknowledgements. OnceDrain registers one native
// drain listener, shared by all currently backpressured writes on that stream.
type Stream struct {
	Write     func([]byte, func(error)) (bool, error)
	Destroyed func() bool
	Destroy   func()
	OnceDrain func(func())
}

type pendingWrite struct {
	stream       *Stream
	bytes        int
	settled      bool
	callbackDone bool
	resultKnown  bool
	waitingDrain bool
}

type Manager struct {
	mu       sync.Mutex
	pending  map[*pendingWrite]struct{}
	draining map[*Stream]bool
	bytes    int
	changed  chan struct{}
}

func New() *Manager {
	return &Manager{pending: make(map[*pendingWrite]struct{}), draining: make(map[*Stream]bool), changed: make(chan struct{})}
}

// settleLocked is idempotent even when EPIPE and a late callback both arrive.
func (m *Manager) settleLocked(write *pendingWrite) {
	if write.settled {
		return
	}
	write.settled = true
	delete(m.pending, write)
	m.bytes -= write.bytes
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *Manager) settleStreamLocked(stream *Stream) {
	for write := range m.pending {
		if write.stream == stream {
			m.settleLocked(write)
		}
	}
}

func (m *Manager) settleIfCompleteLocked(write *pendingWrite) {
	if write.callbackDone && write.resultKnown && !write.waitingDrain {
		m.settleLocked(write)
	}
}

func (m *Manager) callback(write *pendingWrite, failure error) {
	m.mu.Lock()
	broken := errors.Is(failure, syscall.EPIPE)
	if failure != nil {
		m.settleLocked(write)
		if broken {
			m.settleStreamLocked(write.stream)
		}
	} else {
		write.callbackDone = true
		m.settleIfCompleteLocked(write)
	}
	m.mu.Unlock()
	if broken {
		write.stream.Destroy()
	}
}

// Write preserves synchronous error identity. EPIPE settles only this stream,
// destroys it and suppresses future writes. An ordinary callback failure settles
// that write; an ordinary synchronous failure is returned unchanged.
func (m *Manager) Write(stream *Stream, data []byte) error {
	if stream.Destroyed() {
		return nil
	}
	write := &pendingWrite{stream: stream, bytes: len(data)}
	m.mu.Lock()
	m.pending[write] = struct{}{}
	m.bytes += write.bytes
	m.mu.Unlock()
	accepted, failure := stream.Write(append([]byte(nil), data...), func(err error) { m.callback(write, err) })
	if failure != nil {
		m.callback(write, failure)
		if errors.Is(failure, syscall.EPIPE) {
			return nil
		}
		return failure
	}
	m.mu.Lock()
	if write.settled {
		m.mu.Unlock()
		return nil
	}
	write.resultKnown = true
	write.waitingDrain = !accepted
	registerDrain := write.waitingDrain && !m.draining[stream]
	if registerDrain {
		m.draining[stream] = true
	}
	m.settleIfCompleteLocked(write)
	m.mu.Unlock()
	if registerDrain {
		stream.OnceDrain(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			delete(m.draining, stream)
			for pending := range m.pending {
				if pending.stream == stream && pending.waitingDrain {
					pending.waitingDrain = false
					m.settleIfCompleteLocked(pending)
				}
			}
		})
	}
	return nil
}

// HandleError ports the registered stdout/stderr native error listener. Ordinary
// errors do not settle unrelated pending writes; EPIPE does not fail the Agent.
func (m *Manager) HandleError(stream *Stream, failure error) {
	if !errors.Is(failure, syscall.EPIPE) {
		return
	}
	m.mu.Lock()
	m.settleStreamLocked(stream)
	m.mu.Unlock()
	stream.Destroy()
}

func (m *Manager) Budget() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bytes == 0 {
		return 0
	}
	millis := 2000 + math.Ceil(float64(m.bytes)/float64(512*1024)*1000)
	if millis > 30000 {
		millis = 30000
	}
	return time.Duration(millis) * time.Millisecond
}

// Flush observes all queued streams and any writes added before quiescence.
// The bounded deadline returns without discarding pending acknowledgements.
func (m *Manager) Flush(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	m.mu.Lock()
	empty := m.bytes == 0
	m.mu.Unlock()
	if empty {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		m.mu.Lock()
		if len(m.pending) == 0 {
			m.mu.Unlock()
			return
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			return
		}
	}
}
