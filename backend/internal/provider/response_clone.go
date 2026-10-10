package provider

import (
	"io"
	"net"
	"sync"
)

// responseFork is a physical byte fork. Either branch may demand the next
// read; both receive the same bytes/error in order. It owns no SDK decoder,
// model state or owner scope. Closing one branch leaves the other readable.
type responseFork struct {
	mu          sync.Mutex
	ready       *sync.Cond
	reading     bool
	source      io.Reader
	closeSource func() error
	queues      [2][][]byte
	closed      [2]bool
	terminal    error
	reads       sync.WaitGroup
	closeOnce   sync.Once
	closeErr    error
}
type responseBranch struct {
	fork  *responseFork
	index int
}

func forkResponse(source io.Reader, closeSource func() error) (*responseBranch, *responseBranch) {
	f := &responseFork{source: source, closeSource: closeSource}
	f.ready = sync.NewCond(&f.mu)
	return &responseBranch{f, 0}, &responseBranch{f, 1}
}
func (f *responseFork) available(index int, out []byte) (int, error, bool) {
	if f.closed[index] {
		return 0, net.ErrClosed, true
	}
	if len(f.queues[index]) > 0 {
		chunk := f.queues[index][0]
		n := copy(out, chunk)
		if n == len(chunk) {
			f.queues[index][0] = nil
			f.queues[index] = f.queues[index][1:]
		} else {
			f.queues[index][0] = chunk[n:]
		}
		if len(f.queues[index]) == 0 && f.terminal != nil {
			return n, f.terminal, true
		}
		return n, nil, true
	}
	if f.terminal != nil {
		return 0, f.terminal, true
	}
	return 0, nil, false
}
func (branch *responseBranch) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	f := branch.fork
	f.mu.Lock()
	for {
		if n, e, ready := f.available(branch.index, out); ready {
			f.mu.Unlock()
			return n, e
		}
		if !f.reading {
			break
		}
		// A peer may be waiting for the next physical read after publishing
		// this branch's bytes. Wake on publication and recheck its own queue.
		f.ready.Wait()
	}
	f.reading = true
	f.reads.Add(1)
	f.mu.Unlock()
	buffer := make([]byte, len(out))
	n, e := f.source.Read(buffer)
	f.mu.Lock()
	if n > 0 {
		for index := range f.queues {
			if !f.closed[index] {
				f.queues[index] = append(f.queues[index], append([]byte(nil), buffer[:n]...))
			}
		}
	}
	if e != nil {
		f.terminal = e
	}
	n, e, ready := f.available(branch.index, out)
	f.reading = false
	f.ready.Broadcast()
	f.mu.Unlock()
	f.reads.Done()
	if !ready {
		return 0, nil
	}
	return n, e
}
func (branch *responseBranch) Close() error {
	f := branch.fork
	f.mu.Lock()
	f.closed[branch.index] = true
	f.queues[branch.index] = nil
	f.ready.Broadcast()
	both := f.closed[0] && f.closed[1]
	f.mu.Unlock()
	if both {
		f.closeOnce.Do(func() { f.closeErr = f.closeSource(); f.reads.Wait() })
		return f.closeErr
	}
	return nil
}

// cloneFetchBody retains each native SDK lease's identity. Only its private
// physical reader is forked; the tracing branch cannot borrow an owner scope.
func cloneFetchBody(body io.ReadCloser) io.ReadCloser {
	if native, ok := body.(*nativeOwnerBody); ok {
		original := native.physical.body
		sdk, clone := forkResponse(original, native.closeAll)
		native.traceBranch = sdk
		native.ReadCloser = sdk
		native.physical.body = sdk
		return clone
	}
	if owned, ok := body.(*ownerLease); ok {
		owned.mu.Lock()
		original := owned.body
		owned.mu.Unlock()
		sdk, clone := forkResponse(original, owned.closeAll)
		owned.mu.Lock()
		owned.body = sdk
		owned.traceBranch = sdk
		owned.mu.Unlock()
		return clone
	}
	return nil
}

func cloneOrdinaryBody(body io.ReadCloser) (io.ReadCloser, io.ReadCloser) {
	return forkResponse(body, body.Close)
}
