package provider

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Fetch supplies these physical headers after the SDK has built its own header
// object. Preserve explicit caller values without changing that original object.
func nativeFetchRequest(request *http.Request) *http.Request {
	copy := request.Clone(request.Context())
	if copy.Header == nil {
		copy.Header = make(http.Header)
	}
	if len(copy.Header.Values("Accept-Encoding")) == 0 {
		copy.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	}
	if len(copy.Header.Values("Connection")) == 0 {
		copy.Header.Set("Connection", "keep-alive")
	}
	return copy
}

// Decoding belongs to the demanded body read. Construction never reads bytes:
// a completed header must still fulfill fetch and clear its header timeout.
func nativeFetchBody(body io.ReadCloser, encoding, endpoint string) io.ReadCloser {
	switch encoding {
	case "gzip", "deflate", "br", "zstd":
		return &fetchDecodedBody{upstream: body, encoding: encoding, endpoint: endpoint}
	default:
		return body
	}
}

type fetchDecodedBody struct {
	upstream io.ReadCloser
	encoding string
	endpoint string
	encoded  *fetchEncodedReader
	mu       sync.Mutex
	reader   io.Reader
	closer   io.Closer
	failure  error
	closed   bool
	once     sync.Once
}

func (body *fetchDecodedBody) initialize() {
	body.encoded = &fetchEncodedReader{Reader: body.upstream}
	reader := bufio.NewReader(body.encoded)
	if _, err := reader.Peek(1); err != nil {
		body.failure = err
		return
	}
	switch body.encoding {
	case "gzip":
		decoder, err := gzip.NewReader(reader)
		body.failure = err
		if err == nil {
			body.reader, body.closer = decoder, decoder
		}
	case "deflate":
		prefix, _ := reader.Peek(2)
		if len(prefix) == 2 && prefix[0]&15 == 8 && (int(prefix[0])<<8|int(prefix[1]))%31 == 0 {
			decoder, err := zlib.NewReader(reader)
			body.reader, body.closer, body.failure = decoder, decoder, err
		} else {
			decoder := flate.NewReader(reader)
			body.reader, body.closer = decoder, decoder
		}
	case "br":
		body.reader = brotli.NewReader(reader)
	case "zstd":
		decoder, err := zstd.NewReader(reader, zstd.WithDecoderConcurrency(1))
		body.reader, body.failure = decoder, err
		if err == nil {
			body.closer = fetchDecoderClose(func() { decoder.Close() })
		}
	}
}

func (body *fetchDecodedBody) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.closed {
		return 0, io.ErrClosedPipe
	}
	if body.reader == nil && body.failure == nil {
		body.initialize()
	}
	if body.failure != nil {
		return 0, body.readFailure(body.failure)
	}
	n, err := body.reader.Read(out)
	return n, body.readFailure(err)
}

func (body *fetchDecodedBody) Close() error {
	var err error
	body.once.Do(func() {
		err = body.upstream.Close()
		body.mu.Lock()
		defer body.mu.Unlock()
		body.closed = true
		if body.closer != nil {
			_ = body.closer.Close()
		}
	})
	return err
}

type fetchDecoderClose func()

func (close fetchDecoderClose) Close() error { close(); return nil }

// Preserve physical socket faults; codec failures retain Bun's fetch error
// identity and URL, independent of the concrete Go decompressor's wording.
type NativeCompressionReadError struct {
	cause          error
	code, endpoint string
}

func (e *NativeCompressionReadError) Error() string {
	return fmt.Sprintf("%s fetching %q. For more information, pass `verbose: true` in the second argument to fetch()", e.code, e.endpoint)
}
func (e *NativeCompressionReadError) Code() string  { return e.code }
func (e *NativeCompressionReadError) Unwrap() error { return e.cause }

type fetchEncodedReader struct {
	io.Reader
	failure error
}

func (reader *fetchEncodedReader) Read(out []byte) (int, error) {
	n, err := reader.Reader.Read(out)
	if err != nil && err != io.EOF {
		reader.failure = err
	}
	return n, err
}
func (body *fetchDecodedBody) readFailure(err error) error {
	if err == nil || err == io.EOF {
		return err
	}
	if body.encoded != nil && body.encoded.failure != nil && errors.Is(err, body.encoded.failure) {
		return err
	}
	code := "ZlibError"
	if body.encoding == "br" {
		code = "BrotliDecompressionError"
	}
	if body.encoding == "zstd" {
		code = "ZstdDecompressionError"
	}
	return &NativeCompressionReadError{cause: err, code: code, endpoint: body.endpoint}
}
