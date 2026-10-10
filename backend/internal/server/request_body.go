package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// The original main Bun listener leaves maxRequestBodySize at its pinned
// 1.3.14 default. Actual HTTP boundary tests establish 128MiB is admitted and
// 128MiB+1 is rejected before the API producer. This is an ingress bound, not
// a replacement for smaller consumer limits (e.g. voice's 4MiB clip limit).
const apiBodyLimit int64 = 128 * 1024 * 1024
const apiBodyMemoryLimit = 64 * 1024

var errAPIBodyLimit = errors.New("request body exceeds original main listener limit")

// Reading ahead is required for net/http to detect a disconnected peer while
// an API awaits something before its first body read. Its original reqBody is
// also closed by finishRequest: the detached consumer must read owned bytes.
// Memory stays bounded; the unread upload spills to a private 0600 tempfile.
// Reads stream available bytes, retaining consumer limits and partial-body
// errors rather than requiring every route to eagerly ReadAll its input.
type apiRequestBody struct {
	mu               sync.Mutex
	changed          *sync.Cond
	memory           []byte
	file             *os.File
	size, offset     int64
	terminal         error
	finished, closed bool
	done             chan struct{}
	source           io.ReadCloser
	controller       *http.ResponseController
	stopOnce         sync.Once
}

func newAPIRequestBody(source io.ReadCloser, writer http.ResponseWriter, tooLarge func()) *apiRequestBody {
	body := &apiRequestBody{source: source, controller: http.NewResponseController(writer), done: make(chan struct{})}
	body.changed = sync.NewCond(&body.mu)
	// HTTP/1 otherwise drains the original body implicitly at WriteHeader,
	// competing with this sole ingress reader. HTTP/2 is already full duplex.
	_ = body.controller.EnableFullDuplex()
	go body.ingest(tooLarge)
	return body
}
func (body *apiRequestBody) ingest(tooLarge func()) {
	defer close(body.done)
	buffer := make([]byte, 32*1024)
	var terminal error
	for {
		n, err := body.source.Read(buffer)
		if n > 0 {
			body.mu.Lock()
			if body.size+int64(n) > apiBodyLimit {
				terminal = errAPIBodyLimit
			} else if body.file == nil && body.size+int64(n) <= apiBodyMemoryLimit {
				body.memory = append(body.memory, buffer[:n]...)
				body.size += int64(n)
			} else {
				if body.file == nil {
					body.file, terminal = os.CreateTemp("", "cc-haha-api-body-*")
					if terminal == nil {
						_, terminal = body.file.Write(body.memory)
						body.memory = nil
					}
				}
				if terminal == nil {
					var written int
					written, terminal = body.file.Write(buffer[:n])
					body.size += int64(written)
				}
			}
			body.changed.Broadcast()
			body.mu.Unlock()
			if terminal != nil {
				break
			}
		}
		if err != nil {
			terminal = err
			break
		}
	}
	// The native listener's size response must win before publishing the
	// terminal read error to a JSON consumer that would otherwise emit 400.
	if errors.Is(terminal, errAPIBodyLimit) {
		tooLarge()
	}
	body.mu.Lock()
	body.terminal = terminal
	body.finished = true
	body.changed.Broadcast()
	body.mu.Unlock()
}
func (body *apiRequestBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	body.mu.Lock()
	defer body.mu.Unlock()
	for !body.closed && body.offset == body.size && !body.finished {
		body.changed.Wait()
	}
	if body.closed {
		return 0, http.ErrBodyReadAfterClose
	}
	if body.offset < body.size {
		n := min(int64(len(p)), body.size-body.offset)
		if body.file == nil {
			copy(p, body.memory[body.offset:body.offset+n])
		} else if _, err := body.file.ReadAt(p[:n], body.offset); err != nil {
			return 0, err
		}
		body.offset += n
		return int(n), nil
	}
	return 0, body.terminal
}

// stopIngress is only called while ServeHTTP still owns its response writer.
// SetReadDeadline interrupts a physical partial upload; joining the reader
// before returning prevents net/http's finishRequest closing it concurrently.
func (body *apiRequestBody) receivedEOF() bool {
	body.mu.Lock()
	defer body.mu.Unlock()
	return body.finished && errors.Is(body.terminal, io.EOF)
}

func (body *apiRequestBody) stopIngress() {
	body.stopOnce.Do(func() {
		select {
		case <-body.done:
		default:
			if !body.receivedEOF() {
				_ = body.controller.SetReadDeadline(time.Now())
			}
			<-body.done
		}
		body.mu.Lock()
		eof := errors.Is(body.terminal, io.EOF)
		body.mu.Unlock()
		// EOF Close is purely local. A read deadline here could race the
		// keep-alive background read and cancel the entire pooled connection.
		// On a spool/size failure EOF was not consumed; prevent Close from
		// trying to drain an incomplete upload indefinitely.
		if !eof {
			_ = body.controller.SetReadDeadline(time.Now())
		}
		_ = body.source.Close()
		if !eof {
			_ = body.controller.SetReadDeadline(time.Time{})
		}
	})
}
func (body *apiRequestBody) Close() error {
	body.mu.Lock()
	body.closed = true
	body.changed.Broadcast()
	body.mu.Unlock()
	return nil
}

// release runs after both ingress and business have joined. It must not be
// replaced by ordinary ReadCloser.Close, which a consumer may call earlier.
func (body *apiRequestBody) release() error {
	<-body.done
	body.mu.Lock()
	defer body.mu.Unlock()
	body.closed = true
	body.memory = nil
	body.changed.Broadcast()
	if body.file != nil {
		name := body.file.Name()
		return errors.Join(body.file.Close(), os.Remove(name))
	}
	return nil
}

var _ io.ReadCloser = (*apiRequestBody)(nil)
