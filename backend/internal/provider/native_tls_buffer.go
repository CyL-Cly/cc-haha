package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
)

type nativeTLSWriteReadiness interface {
	wait() error
	wake()
	close()
}

// This queue owns copies of already encrypted records. The only blocking work
// is on its pure descriptor worker; an owner-side TLS alert/key update Write
// never waits for socket writability or re-encrypts/retries application bytes.
type nativeTLSRecordWriter struct {
	mu              sync.Mutex
	socket          *nativeHTTP1Socket
	waiter          nativeTLSWriteReadiness
	queue           [][]byte
	offset, pending int
	closed          bool
	failure         error
	changed         chan struct{}
	work            chan struct{}
	done            chan struct{}
	callbackDone    chan struct{}
	stopCallback    func() bool
	onFailure       func(error)
	closeOnce       sync.Once
}

const nativeTLSMaxPendingRecords = 9 * 1024 * 1024

func newNativeTLSRecordWriter(ctx context.Context, socket *nativeHTTP1Socket, onFailure func(error)) (*nativeTLSRecordWriter, error) {
	if ctx == nil || socket == nil || onFailure == nil {
		return nil, errors.New("native TLS writer requires owned context/socket/failure consumer")
	}
	waiter, err := newNativeTLSWriteReadiness(socket)
	if err != nil {
		return nil, err
	}
	w := &nativeTLSRecordWriter{socket: socket, waiter: waiter, changed: make(chan struct{}), work: make(chan struct{}, 1), done: make(chan struct{}), callbackDone: make(chan struct{}), onFailure: onFailure}
	w.stopCallback = context.AfterFunc(ctx, func() { w.fail(ctx.Err()); close(w.callbackDone) })
	go w.run()
	return w, nil
}
func (w *nativeTLSRecordWriter) signalLocked() {
	close(w.changed)
	w.changed = make(chan struct{})
	select {
	case w.work <- struct{}{}:
	default:
	}
}
func (w *nativeTLSRecordWriter) enqueue(out []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, net.ErrClosed
	}
	if w.failure != nil {
		return 0, w.failure
	}
	if len(out) == 0 {
		return 0, nil
	}
	if len(out) > nativeTLSMaxPendingRecords-w.pending {
		return 0, fmt.Errorf("%w: bounded owned TLS encrypted-record queue", ErrHTTPExchangeProfile)
	}
	w.queue = append(w.queue, append([]byte(nil), out...))
	w.pending += len(out)
	w.signalLocked()
	return len(out), nil
}
func (w *nativeTLSRecordWriter) fail(err error) {
	if err == nil {
		err = net.ErrClosed
	}
	w.mu.Lock()
	if w.closed || w.failure != nil {
		w.mu.Unlock()
		return
	}
	w.failure = err
	w.signalLocked()
	w.mu.Unlock()
	w.waiter.wake()
	w.onFailure(err)
}
func (w *nativeTLSRecordWriter) run() {
	defer close(w.done)
	for {
		w.mu.Lock()
		if w.closed || w.failure != nil {
			w.mu.Unlock()
			return
		}
		if len(w.queue) == 0 {
			w.mu.Unlock()
			<-w.work
			continue
		}
		out := w.queue[0][w.offset:]
		w.mu.Unlock()
		count, blocked, err := nativeTLSWriteSome(w.socket, out)
		if count > 0 {
			w.mu.Lock()
			if !w.closed {
				w.offset += count
				w.pending -= count
				if w.offset == len(w.queue[0]) {
					w.queue[0] = nil
					w.queue = w.queue[1:]
					w.offset = 0
				}
				w.signalLocked()
			}
			w.mu.Unlock()
		}
		if err != nil {
			w.fail(err)
			return
		}
		if blocked {
			w.mu.Lock()
			stopped := w.closed || w.failure != nil
			w.mu.Unlock()
			if stopped {
				return
			}
			if err = w.waiter.wait(); err != nil {
				w.fail(err)
				return
			}
		}
	}
}
func (w *nativeTLSRecordWriter) drain(ctx context.Context) error {
	for {
		w.mu.Lock()
		failure, closed, pending, changed := w.failure, w.closed, w.pending, w.changed
		w.mu.Unlock()
		if failure != nil {
			return failure
		}
		if closed {
			return net.ErrClosed
		}
		if pending == 0 {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (w *nativeTLSRecordWriter) close() {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		w.queue = nil
		w.pending = 0
		w.signalLocked()
		w.mu.Unlock()
		w.waiter.wake()
		if !w.stopCallback() {
			<-w.callbackDone
		}
		<-w.done
		w.waiter.close()
	})
}
