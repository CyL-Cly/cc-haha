package imruntime

import (
	"sync"
	"time"
)

// MessageBuffer retains the original timer/threshold/active-complete ordering.
type MessageBuffer struct {
	mu                                sync.Mutex
	buffer                            Text
	interval                          time.Duration
	threshold                         int
	timer                             *bufferTimer
	flushing, pendingComplete, closed bool
	active                            chan struct{}
	flush                             func(Text, bool) error
	log                               func(error)
	wg                                sync.WaitGroup
}
type bufferTimer struct {
	timer  *time.Timer
	cancel chan struct{}
	once   sync.Once
}

func (t *bufferTimer) stop() { t.once.Do(func() { t.timer.Stop(); close(t.cancel) }) }
func NewMessageBuffer(flush func(Text, bool) error, interval time.Duration, threshold int, log func(error)) *MessageBuffer {
	if interval == 0 {
		interval = 500 * time.Millisecond
	}
	if threshold == 0 {
		threshold = 200
	}
	return &MessageBuffer{flush: flush, interval: interval, threshold: threshold, log: log}
}
func (b *MessageBuffer) stopTimerLocked() {
	if b.timer != nil {
		b.timer.stop()
		b.timer = nil
	}
}
func (b *MessageBuffer) Append(text Text) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.buffer = b.buffer.Join(text)
	if len(b.buffer) >= b.threshold {
		b.stopTimerLocked()
		b.launchLocked(false)
	} else if b.timer == nil {
		timer := &bufferTimer{timer: time.NewTimer(b.interval), cancel: make(chan struct{})}
		b.timer = timer
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			select {
			case <-timer.cancel:
				return
			case <-timer.timer.C:
			}
			b.mu.Lock()
			if b.timer == timer {
				b.timer = nil
			}
			if !b.closed {
				b.launchLocked(false)
			}
			b.mu.Unlock()
		}()
	}
}
func (b *MessageBuffer) launchLocked(complete bool) chan struct{} {
	b.stopTimerLocked()
	if b.flushing {
		if complete {
			b.pendingComplete = true
		}
		return b.active
	}
	if len(b.buffer) == 0 {
		return nil
	}
	b.flushing = true
	data := b.buffer.Clone()
	b.buffer = Text{}
	done := make(chan struct{})
	b.active = done
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		for {
			if err := b.flush(data, complete); err != nil && b.log != nil {
				b.log(err)
			}
			b.mu.Lock()
			if b.pendingComplete {
				b.pendingComplete = false
				complete = true
				data = b.buffer.Clone()
				b.buffer = Text{}
				if len(data) > 0 {
					b.mu.Unlock()
					continue
				}
			}
			b.flushing = false
			b.active = nil
			close(done)
			b.mu.Unlock()
			return
		}
	}()
	return done
}
func (b *MessageBuffer) Complete() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.stopTimerLocked()
	done := b.launchLocked(true)
	b.mu.Unlock()
	if done != nil {
		<-done
	}
}
func (b *MessageBuffer) Reset() {
	b.mu.Lock()
	b.buffer = Text{}
	b.pendingComplete = false
	b.stopTimerLocked()
	b.mu.Unlock()
}
func (b *MessageBuffer) Close() {
	b.mu.Lock()
	b.closed = true
	b.buffer = Text{}
	b.pendingComplete = false
	b.stopTimerLocked()
	b.mu.Unlock()
	b.wg.Wait()
}
