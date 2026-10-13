package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Source index.ts races the response with request.signal, without passing
// that signal to ordinary API business promises. Explicit signal consumers
// obtain their separate context with requestSignalContext below.
type apiSignalContextKey struct{}
type apiOperationContext struct {
	context.Context
	values context.Context
	signal context.Context
}

func (ctx apiOperationContext) Value(key any) any {
	if _, ok := key.(apiSignalContextKey); ok {
		return ctx.signal
	}
	return ctx.values.Value(key)
}
func requestSignalContext(request *http.Request) context.Context {
	if signal, ok := request.Context().Value(apiSignalContextKey{}).(context.Context); ok {
		return signal
	}
	return request.Context()
}
func (server *Server) beginAPIOperation(request *http.Request) (*http.Request, func(), bool) {
	server.mu.Lock()
	if server.closed || server.migration.active {
		server.mu.Unlock()
		return nil, nil, false
	}
	server.apiOperations.Add(1)
	server.mu.Unlock()
	signal, cancel := context.WithCancelCause(request.Context())
	stop := context.AfterFunc(server.ctx, func() { cancel(context.Cause(server.ctx)) })
	ctx := apiOperationContext{Context: server.ctx, values: context.WithoutCancel(request.Context()), signal: signal}
	return request.Clone(ctx), func() { stop(); cancel(context.Canceled); server.apiOperations.Done() }, true
}

type apiResponseHead struct {
	status int
	header http.Header
}
type apiResponseChunk struct {
	data  []byte
	flush bool
}

// Only this bridge is used by the business goroutine. No net/http writer is
// retained after ServeHTTP returns. Bounded chunks also retain streaming/raw
// file response behavior instead of buffering a potentially large body.
type apiResponseBridge struct {
	header             http.Header
	head               apiResponseHead
	ready              chan struct{}
	wake               chan struct{}
	chunks             chan apiResponseChunk
	delivery           context.Context
	inputRejected      atomic.Bool
	winner             atomic.Int32 // 1=response, 2=client/server abort before response
	headOnce, wakeOnce sync.Once
}

func (w *apiResponseBridge) Header() http.Header { return w.header }
func (w *apiResponseBridge) choose(winner int32) {
	if w.winner.CompareAndSwap(0, winner) {
		w.wakeOnce.Do(func() { close(w.wake) })
	}
}
func (w *apiResponseBridge) WriteHeader(status int) {
	w.headOnce.Do(func() {
		w.head = apiResponseHead{status, w.header.Clone()}
		close(w.ready)
		if w.delivery.Err() != nil {
			w.choose(2)
		} else {
			w.choose(1)
		}
	})
}
func (w *apiResponseBridge) send(chunk apiResponseChunk) error {
	select {
	case <-w.delivery.Done():
		return context.Cause(w.delivery)
	case w.chunks <- chunk:
		return nil
	}
}
func (w *apiResponseBridge) Write(data []byte) (int, error) {
	if w.inputRejected.Load() {
		return 0, errAPIBodyLimit
	}
	w.WriteHeader(http.StatusOK)
	written := 0
	for len(data) > 0 {
		length := min(len(data), 32*1024)
		if err := w.send(apiResponseChunk{data: append([]byte(nil), data[:length]...)}); err != nil {
			return written, err
		}
		written += length
		data = data[length:]
	}
	return written, nil
}
func (w *apiResponseBridge) FlushError() error {
	if w.inputRejected.Load() {
		return errAPIBodyLimit
	}
	w.WriteHeader(http.StatusOK)
	return w.send(apiResponseChunk{flush: true})
}
func (w *apiResponseBridge) Flush() { _ = w.FlushError() }

// The main listener alone applies the response race. Source's paired-device
// business listener and SDK collaboration actions call the same API producer
// directly, so those callers use settle=false.
func (server *Server) serveAPILifecycle(writer http.ResponseWriter, request *http.Request, settle bool, operation func(http.ResponseWriter, *http.Request)) {
	if settle && request.ContentLength > apiBodyLimit {
		writer.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	bound, done, ok := server.beginAPIOperation(request)
	if !ok {
		server.migrationAdmissionFailure(writer)
		return
	}
	if !settle {
		defer done()
		operation(writer, bound)
		return
	}
	// Only the client's signal wins Source's response499 race. Server.Close
	// cancels business work, whose own original error response still settles.
	delivery, cancel := context.WithCancelCause(requestSignalContext(request))
	defer cancel(context.Canceled)
	bridge := &apiResponseBridge{header: writer.Header().Clone(), ready: make(chan struct{}), wake: make(chan struct{}), chunks: make(chan apiResponseChunk, 1), delivery: delivery}
	if delivery.Err() != nil {
		bridge.choose(2)
	}
	stopAbort := context.AfterFunc(delivery, func() { bridge.choose(2) })
	defer stopAbort()
	var body *apiRequestBody
	if request.Body != nil && request.Body != http.NoBody {
		body = newAPIRequestBody(request.Body, writer, func() { bridge.inputRejected.Store(true); bridge.WriteHeader(http.StatusRequestEntityTooLarge) })
		bound.Body = body
	}
	ingressAbortDone := make(chan struct{})
	stopIngressAbort := func() bool { return true }
	if body != nil {
		stopIngressAbort = context.AfterFunc(server.ctx, func() { body.stopIngress(); close(ingressAbortDone) })
	}
	defer func() {
		if body != nil {
			// Keep the server cancellation relay registered until this actual
			// upload has joined. An early response can precede its completion.
			<-body.done
			body.stopIngress()
		}
		if !stopIngressAbort() {
			<-ingressAbortDone
		}
	}()
	go func() {
		defer done()
		if body != nil {
			defer body.release()
		}
		defer close(bridge.chunks)
		// Keep an API panic confined to the operation; Source's async route
		// errors become 500 responses rather than escaping its fetch handler.
		defer func() {
			if recover() != nil {
				writeError(bridge, errors.New("API operation panic"))
			}
		}()
		operation(bridge, bound)
		bridge.WriteHeader(http.StatusOK)
	}()
	<-bridge.wake
	if bridge.winner.Load() == 2 {
		writer.WriteHeader(499)
		return
	}
	<-bridge.ready
	for name, values := range bridge.head.header {
		writer.Header()[name] = append([]string(nil), values...)
	}

	writer.WriteHeader(bridge.head.status)
	controller := http.NewResponseController(writer)
	writeAbortDone := make(chan struct{})
	stopWriteAbort := context.AfterFunc(delivery, func() { _ = controller.SetWriteDeadline(time.Now()); close(writeAbortDone) })
	serverWriteAbortDone := make(chan struct{})
	stopServerWriteAbort := func() bool { return true }
	if server.ctx.Err() == nil {
		stopServerWriteAbort = context.AfterFunc(server.ctx, func() { _ = controller.SetWriteDeadline(time.Now()); close(serverWriteAbortDone) })
	}
	defer func() {
		if !stopWriteAbort() {
			<-writeAbortDone
		}
		if !stopServerWriteAbort() {
			<-serverWriteAbortDone
		}
		_ = controller.SetWriteDeadline(time.Time{})
	}()
	for {
		select {
		case <-delivery.Done():
			return
		case chunk, ok := <-bridge.chunks:
			if !ok {
				// JSON/file responses have a known Content-Length. Let the
				// peer consume the completed response even if its own upload
				// is still being received, as the original Bun listener does.
				_ = controller.Flush()
				return
			}
			if len(chunk.data) > 0 {
				if _, err := writer.Write(chunk.data); err != nil {
					return
				}
			}
			if chunk.flush {
				if err := controller.Flush(); err != nil {
					return
				}
			}
		}
	}
}
func (server *Server) waitAPIOperations() {
	server.apiOperations.Wait()
}
