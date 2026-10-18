package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

// NativeHTTP1Operation is an explicit fresh direct-TCP response-byte resource.
// The request was written once before construction. No other reader may consume
// that connection afterward. Checkpoint consumes currently available native
// bytes without waiting; WaitReadable only waits, never reads/parses/calls policy.
// Owner-side http.ReadResponse adopts the same retained prefix through Reader.
// TLS, proxies, pooled connections and SDK/timer policy are outside this API.
// nativeHTTP1RecordReader is a transport-only optional TLS decoder. The native
// socket remains the same concrete resource, descriptor and readiness owner.
type nativeHTTP1RecordReader interface{ read([]byte) (int, error) }
type nativeHTTP1RecordCloser interface{ closeTransport() }

type NativeHTTP1Operation struct {
	mu           sync.Mutex
	readMu       sync.Mutex
	connection   *net.TCPConn
	socket       *nativeHTTP1Socket
	recordReader nativeHTTP1RecordReader
	limit        int
	bytes        []byte
	position     int
	headerEnd    int
	terminal     error
	adopted      bool
	closed       bool
	waiting      bool
	waits        sync.WaitGroup
	closeOnce    sync.Once
	closeErr     error
	reader       *bufio.Reader
}

type NativeHTTP1State string

const (
	NativeHTTP1NoReadable     NativeHTTP1State = "no-readable"
	NativeHTTP1Partial        NativeHTTP1State = "partial"
	NativeHTTP1HeaderComplete NativeHTTP1State = "header-complete"
	NativeHTTP1Fault          NativeHTTP1State = "fault"
)

type NativeHTTP1Checkpoint struct {
	State  NativeHTTP1State
	Header []byte
	// Received is newly consumed bytes, not the buffered prefix length. Err is
	// an original native error/EOF or explicit finite framing capability error.
	Received int
	Err      error
}

func NewNativeHTTP1Operation(connection net.Conn, headerLimit int) (*NativeHTTP1Operation, error) {
	tcp, ok := connection.(*net.TCPConn)
	if !ok || tcp == nil {
		return nil, fmt.Errorf("%w: native checkpoint requires direct TCP without TLS", ErrHTTPExchangeProfile)
	}
	if headerLimit < 4 || headerLimit > 1024*1024 {
		return nil, fmt.Errorf("%w: native bounded header capture", ErrHTTPExchangeProfile)
	}
	socket, err := newNativeHTTP1Socket(tcp)
	if err != nil {
		return nil, err
	}
	return &NativeHTTP1Operation{connection: tcp, socket: socket, limit: headerLimit}, nil
}

func (operation *NativeHTTP1Operation) readNativeBytes(out []byte) (int, error) {
	if operation.recordReader != nil {
		return operation.recordReader.read(out)
	}
	return operation.socket.read(out)
}

func (operation *NativeHTTP1Operation) Checkpoint() NativeHTTP1Checkpoint {
	operation.mu.Lock()
	defer operation.mu.Unlock()
	if operation.closed {
		return NativeHTTP1Checkpoint{State: NativeHTTP1Fault, Err: net.ErrClosed}
	}
	if operation.adopted {
		return NativeHTTP1Checkpoint{State: NativeHTTP1Fault, Err: fmt.Errorf("%w: header checkpoint after response adoption", ErrHTTPExchangeProfile)}
	}
	result := NativeHTTP1Checkpoint{}
	for operation.headerEnd == 0 && operation.terminal == nil {
		scratch := make([]byte, 32*1024)
		count, err := operation.readNativeBytes(scratch)
		if count > 0 {
			result.Received += count
			operation.bytes = append(operation.bytes, scratch[:count]...)
			if offset := bytes.Index(operation.bytes, []byte("\r\n\r\n")); offset >= 0 {
				operation.headerEnd = offset + 4
				if operation.headerEnd > operation.limit {
					operation.terminal = fmt.Errorf("%w: response headers exceed capture bound", ErrHTTPExchangeProfile)
					operation.headerEnd = 0
				}
			} else if len(operation.bytes) >= operation.limit {
				operation.terminal = fmt.Errorf("%w: response headers exceed capture bound", ErrHTTPExchangeProfile)
			}
		}
		if err != nil {
			operation.terminal = err
		}
		if count == 0 {
			break
		}
	}
	switch {
	case operation.headerEnd > 0:
		result.State = NativeHTTP1HeaderComplete
		result.Header = append([]byte(nil), operation.bytes[:operation.headerEnd]...)
		// A sleeping readiness worker must return even if this checkpoint consumed
		// all kernel bytes before it was scheduled. This is only resource wakeup.
		operation.socket.wake()
	case operation.terminal != nil:
		result.State = NativeHTTP1Fault
	case len(operation.bytes) > 0:
		result.State = NativeHTTP1Partial
	default:
		result.State = NativeHTTP1NoReadable
	}
	result.Err = operation.terminal
	return result
}

// WaitReadable is pure physical readiness. One demand may wait at a time. A
// cancellation wakes the private native wait without consuming socket data or
// closing an adopted body. Lease cancellation must explicitly call Close.
func (operation *NativeHTTP1Operation) WaitReadable(ctx context.Context) error {
	operation.mu.Lock()
	if operation.closed {
		operation.mu.Unlock()
		return net.ErrClosed
	}
	if operation.waiting {
		operation.mu.Unlock()
		return fmt.Errorf("%w: concurrent readiness wait", ErrHTTPExchangeProfile)
	}
	if err := ctx.Err(); err != nil {
		operation.mu.Unlock()
		return err
	}
	operation.waiting = true
	operation.waits.Add(1)
	operation.mu.Unlock()
	defer func() { operation.mu.Lock(); operation.waiting = false; operation.mu.Unlock(); operation.waits.Done() }()
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { operation.socket.wake(); close(callbackDone) })
	defer func() {
		if !stop() {
			<-callbackDone
		}
	}()
	err := operation.socket.wait()
	if cancel := ctx.Err(); cancel != nil {
		return cancel
	}
	operation.mu.Lock()
	closed := operation.closed
	operation.mu.Unlock()
	if closed {
		return net.ErrClosed
	}
	return err
}

// AdoptResponse belongs to the owner continuation. It uses the existing native
// HTTP framing parser and the same byte operation, including any body prefix.
// It does not perform Bun Headers projection, SDK status policy or timer clear.
func (operation *NativeHTTP1Operation) AdoptResponse(request *http.Request) (*http.Response, error) {
	operation.mu.Lock()
	if operation.closed {
		operation.mu.Unlock()
		return nil, net.ErrClosed
	}
	if operation.adopted || operation.headerEnd == 0 {
		operation.mu.Unlock()
		return nil, fmt.Errorf("%w: response adoption requires one complete header checkpoint", ErrHTTPExchangeProfile)
	}
	operation.adopted = true
	operation.mu.Unlock()
	reader := bufio.NewReader(operation)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		_ = operation.Close()
		return nil, err
	}
	operation.mu.Lock()
	operation.reader = reader
	operation.mu.Unlock()
	response.Body = &nativeHTTP1Body{ReadCloser: response.Body, operation: operation}
	return response, nil
}

// Read is reserved for the owner-adopted response parser/body. The mutex guards
// byte position, while native waits hold neither it nor any business state.
func (operation *NativeHTTP1Operation) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	operation.readMu.Lock()
	defer operation.readMu.Unlock()
	for {
		operation.mu.Lock()
		if operation.closed {
			operation.mu.Unlock()
			return 0, net.ErrClosed
		}
		if !operation.adopted {
			operation.mu.Unlock()
			return 0, fmt.Errorf("%w: response reader before adoption", ErrHTTPExchangeProfile)
		}
		if operation.position < len(operation.bytes) {
			count := copy(out, operation.bytes[operation.position:])
			operation.position += count
			operation.mu.Unlock()
			return count, nil
		}
		if operation.terminal != nil {
			err := operation.terminal
			operation.mu.Unlock()
			return 0, err
		}
		count, err := operation.readNativeBytes(out)
		if err != nil {
			operation.terminal = err
		}
		operation.mu.Unlock()
		if count > 0 || err != nil {
			return count, err
		}
		if err = operation.WaitReadable(context.Background()); err != nil {
			return 0, err
		}
	}
}

func (operation *NativeHTTP1Operation) Close() error {
	operation.closeOnce.Do(func() {
		operation.mu.Lock()
		operation.closed = true
		operation.socket.wake()
		operation.socket.shutdown()
		operation.closeErr = operation.connection.Close()
		operation.mu.Unlock()
		operation.waits.Wait()
		if transport, ok := operation.recordReader.(nativeHTTP1RecordCloser); ok {
			transport.closeTransport()
		}
		operation.mu.Lock()
		operation.socket.close()
		operation.mu.Unlock()
	})
	return operation.closeErr
}

type nativeHTTP1Body struct {
	io.ReadCloser
	operation *NativeHTTP1Operation
}

func (body *nativeHTTP1Body) Close() error { return body.operation.Close() }

var _ io.Reader = (*NativeHTTP1Operation)(nil)
var _ io.Closer = (*NativeHTTP1Operation)(nil)
