package providerproxytransport

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

type decodedBody struct {
	io.Reader
	upstream  io.ReadCloser
	closers   []io.Closer
	closeOnce sync.Once
}

func (b *decodedBody) Close() error {
	b.closeOnce.Do(func() {
		_ = b.upstream.Close()
		for _, closer := range b.closers {
			_ = closer.Close()
		}
	})
	return nil
}
func contentEncodings(header string) []string {
	result := []string{}
	for _, v := range strings.Split(header, ",") {
		v = strings.ToLower(protocol.TrimJavaScriptString(v))
		if v != "" && v != "identity" {
			result = append(result, v)
		}
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// DecodeFetchBody supplies fetch's entity decoding to native HTTP consumers.
// The returned body's Close owns the decoder and original upstream body.
func DecodeFetchBody(body io.ReadCloser, encoding string) (io.ReadCloser, error) {
	decoded := &decodedBody{Reader: body, upstream: body}
	for _, codec := range contentEncodings(encoding) {
		switch codec {
		case "gzip", "x-gzip":
			r, e := gzip.NewReader(decoded.Reader)
			if e != nil {
				return nil, e
			}
			decoded.Reader = r
			decoded.closers = append(decoded.closers, r)
		case "deflate":
			r, e := zlib.NewReader(decoded.Reader)
			if e != nil {
				return nil, e
			}
			decoded.Reader = r
			decoded.closers = append(decoded.closers, r)
		case "br":
			decoded.Reader = brotli.NewReader(decoded.Reader)
		case "zstd":
			r, e := zstd.NewReader(decoded.Reader, zstd.WithDecoderConcurrency(1))
			if e != nil {
				return nil, e
			}
			decoded.Reader = r
			decoded.closers = append(decoded.closers, closeFunc(func() error { r.Close(); return nil }))
		default:
			return body, nil
		}
	}
	return decoded, nil
}

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

type onceBody struct {
	io.ReadCloser
	once sync.Once
}

func (b *onceBody) Close() error {
	var err error
	b.once.Do(func() { err = b.ReadCloser.Close() })
	return err
}
func decodeTraceBytes(raw []byte, encoding string) string {
	for _, codec := range contentEncodings(encoding) {
		var reader io.ReadCloser
		var err error
		switch codec {
		case "gzip", "x-gzip":
			reader, err = gzip.NewReader(bytes.NewReader(raw))
		case "deflate":
			reader, err = zlib.NewReader(bytes.NewReader(raw))
		default:
			return "[trace body unavailable: unsupported content encoding]"
		}
		if err != nil {
			return "[trace body unavailable: decompression failed]"
		}
		raw, err = io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return "[trace body unavailable: decompression failed]"
		}
	}
	decoded := protocol.DecodeJavaScriptUTF8(raw)
	return string(bytes.TrimPrefix(decoded, []byte{0xef, 0xbb, 0xbf}))
}

type idleBody struct {
	upstream  io.ReadCloser
	timeout   time.Duration
	mu        sync.Mutex
	timer     *time.Timer
	failure   error
	closed    bool
	closeOnce sync.Once
}

func newIdleBody(body io.ReadCloser, timeout time.Duration) *idleBody {
	b := &idleBody{upstream: body, timeout: timeout}
	b.timer = time.AfterFunc(timeout, func() {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return
		}
		b.failure = fmt.Errorf("Upstream stream idle timeout after %dms", timeout.Milliseconds())
		b.mu.Unlock()
		b.closeOnce.Do(func() { _ = b.upstream.Close() })
	})
	return b
}
func (b *idleBody) Read(buffer []byte) (int, error) {
	n, err := b.upstream.Read(buffer)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failure != nil {
		return 0, b.failure
	}
	if err != nil {
		b.closed = true
		b.timer.Stop()
	} else if n > 0 && !b.closed {
		b.timer.Reset(b.timeout)
	}
	return n, err
}
func (b *idleBody) Close() error {
	b.mu.Lock()
	b.closed = true
	b.timer.Stop()
	b.mu.Unlock()
	b.closeOnce.Do(func() { _ = b.upstream.Close() })
	return nil
}

type observeBody struct {
	io.ReadCloser
	capture *tracecapture.ProxyCapture
}

func (b *observeBody) Read(buffer []byte) (int, error) {
	n, e := b.ReadCloser.Read(buffer)
	if b.capture != nil {
		if n > 0 {
			b.capture.PushUpstream(buffer[:n])
		}
		if e == io.EOF {
			b.capture.FinishTransport("eof")
		} else if e != nil {
			b.capture.FinishTransport("error")
		}
	}
	return n, e
}

// The bounded decoded trace copy never changes the forwarded entity bytes or
// headers. Its pipe provides backpressure and is ended on consumer cancellation.
type traceBody struct {
	io.ReadCloser
	capture     *tracecapture.ProxyCapture
	headers     http.Header
	cleanup     func()
	finalOnce   sync.Once
	decoder     *traceDecoder
	unavailable bool
}
type traceDecoder struct {
	writer    *io.PipeWriter
	done      chan struct{}
	err       error
	truncated bool
	bytes     int64
	stopFeed  bool
}

func newTraceBody(body io.ReadCloser, capture *tracecapture.ProxyCapture, headers http.Header, encoding string, cleanup func()) *traceBody {
	b := &traceBody{ReadCloser: body, capture: capture, headers: headers.Clone(), cleanup: cleanup}
	if capture == nil {
		return b
	}
	codecs := contentEncodings(encoding)
	if len(codecs) == 0 {
		return b
	}
	if len(codecs) != 1 || codecs[0] != "gzip" && codecs[0] != "x-gzip" && codecs[0] != "deflate" {
		b.unavailable = true
		return b
	}
	reader, writer := io.Pipe()
	d := &traceDecoder{writer: writer, done: make(chan struct{})}
	b.decoder = d
	go func() {
		defer close(d.done)
		defer reader.Close()
		var decoded io.ReadCloser
		var e error
		if codecs[0] == "deflate" {
			decoded, e = zlib.NewReader(reader)
		} else {
			decoded, e = gzip.NewReader(reader)
		}
		if e != nil {
			d.err = e
			return
		}
		defer decoded.Close()
		buffer := make([]byte, 32*1024)
		for {
			n, e := decoded.Read(buffer)
			if n > 0 {
				d.bytes += int64(n)
				capture.PushAnthropic(buffer[:n])
				if d.bytes > tracecapture.StreamCaptureBytes {
					d.truncated = true
					return
				}
			}
			if e != nil {
				if e != io.EOF {
					d.err = e
				}
				return
			}
		}
	}()
	return b
}
func (b *traceBody) Read(buffer []byte) (int, error) {
	n, e := b.ReadCloser.Read(buffer)
	if n > 0 && b.capture != nil && !b.unavailable {
		if b.decoder != nil {
			if !b.decoder.stopFeed {
				if _, err := b.decoder.writer.Write(buffer[:n]); err != nil {
					b.decoder.stopFeed = true
				}
			}
		} else {
			b.capture.PushAnthropic(buffer[:n])
		}
	}
	if e == io.EOF {
		b.finalize(nil, "eof")
	} else if e != nil {
		b.finalize(e, "error")
	}
	return n, e
}
func (b *traceBody) finalize(err error, delivery string) {
	b.finalOnce.Do(func() {
		var snapshot *tracecapture.BodySnapshot
		if b.decoder != nil {
			_ = b.decoder.writer.Close()
			<-b.decoder.done
		}
		if b.capture != nil {
			if b.unavailable {
				v := tracecapture.CreateBodySnapshot(encoded("[trace body unavailable: unsupported content encoding]"), tracecapture.PreviewChars, false)
				snapshot = &v
			} else if b.decoder != nil && b.decoder.err != nil && !b.decoder.truncated && delivery == "eof" {
				v := tracecapture.CreateBodySnapshot(encoded("[trace body unavailable: decompression failed]"), tracecapture.PreviewChars, false)
				snapshot = &v
			}
			status := 200
			b.capture.Complete(tracecapture.ProxyOutcome{ResponseStatus: &status, Headers: b.headers, StreamSnapshot: snapshot, StreamDelivery: delivery, Error: err})
		}
		_ = b.ReadCloser.Close()
		b.cleanup()
	})
}
func (b *traceBody) Close() error {
	b.finalize(errors.New("Stream cancelled"), "cancelled")
	return nil
}
