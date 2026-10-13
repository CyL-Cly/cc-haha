package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SDKStreamError retains SDK error ownership. A JSON SyntaxError has no API
// response properties; HTTP and SSE APIErrors keep the entire payload, not
// just a nested error.message. Nil Status means the SDK's undefined status.
type SDKStreamError struct {
	Kind, Class, Name, Message string
	MessageJSON                json.RawMessage
	Status                     *int
	RequestID                  *string
	WorkspaceID                *string
	Type                       json.RawMessage
	Payload                    json.RawMessage
	RawData                    string
	Headers                    http.Header
	OriginalHeaders            *BunHeaders
	Cause                      error
}

func (err *SDKStreamError) Error() string { return err.Message }
func (err *SDKStreamError) Unwrap() error { return err.Cause }

// RawMessageStream owns one HTTP response and one sequential iterator. It
// does not assemble assistant messages, commit tools, retry or build history.
// Close may run concurrently with Next to release a blocked HTTP body read.
type RawMessageStream struct {
	Headers         http.Header
	OriginalHeaders *BunHeaders
	RequestID       *string
	ctx             context.Context
	body            io.ReadCloser
	cancel          context.CancelFunc
	decoder         protocol.SDKFrameDecoder
	diagnose        func(string, any)
	closeOnce       sync.Once
	mu              sync.Mutex
	closed          bool
	owned           bool
	aborted         error
	closeErr        error
	openAI          *openAIStreamBinding
}

// OpenMessagesStream sends already prepared and signed bytes once. Response
// metadata is available before Controller construction and the first event.
// The existing request timeout ends at response headers, as in SDK fetch.
func (client *Anthropic) OpenMessagesStream(ctx context.Context, prepared *MessagesRequest) (*RawMessageStream, error) {
	if client.config.OwnerBodyIO != nil && (client.config.OwnerOpenIO != nil || client.config.OwnerNativeIO != client.config.OwnerBodyIO.native) {
		return nil, fmt.Errorf("%w: ambiguous native response-body owner", ErrHTTPExchangeProfile)
	}
	// A caller's AbortSignal reason is still user-abort to the SDK. Only its
	// own request-option timer produces APIConnectionTimeoutError.
	exchange, cancel, err := client.openPreparedResponse(ctx, prepared, true)
	if err != nil {
		return nil, sdkRequestError(err)
	}
	response := exchange.Response
	var headers http.Header
	if exchange.OriginalHeaders != nil {
		headers = exchange.OriginalHeaders.HTTPHeader()
	} else {
		headers, err = sdkResponseHeaders(response.Header)
	}
	if err != nil {
		cancel()
		_ = response.Body.Close()
		return nil, err
	}
	response.Header = headers
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer cancel()
		defer response.Body.Close()
		var raw []byte
		var readErr error
		if client.config.OwnerBodyIO != nil && !exchange.localBody {
			raw, readErr = client.config.OwnerBodyIO.errorBody(response.Body)
		} else if client.config.OwnerOpenIO != nil && !exchange.localBody {
			raw, readErr = client.config.OwnerOpenIO.errorBody(response.Body)
		} else {
			raw, readErr = io.ReadAll(response.Body)
		}
		var ownerFailure *ownerWaitFailure
		if errors.As(readErr, &ownerFailure) {
			if client.config.OwnerBodyIO != nil {
				// Native demanded body completion retains its administrative
				// marker across retry wrappers. Legacy callers own the raw cause.
				return nil, readErr
			}
			return nil, ownerFailure.cause
		}
		if readErr != nil {
			// SDK response.text().catch uses the read error's message as body.
			raw = []byte(readErr.Error())
		}
		text := protocol.DecodeJavaScriptUTF8(raw)
		text = bytes.TrimPrefix(text, []byte("\xef\xbb\xbf"))
		if exchange.OriginalHeaders != nil {
			return nil, NewSDKResponseError(&response.StatusCode, string(text), exchange.OriginalHeaders, true)
		}
		return nil, sdkAPIError(&response.StatusCode, string(text), response.Header, true)
	}
	body := response.Body
	if client.config.OwnerBodyIO != nil && !exchange.localBody {
		body, err = client.config.OwnerBodyIO.streamBody(body)
	} else if client.config.OwnerOpenIO != nil && !exchange.localBody {
		body, err = client.config.OwnerOpenIO.streamBody(body)
	}
	if err != nil {
		cancel()
		_ = response.Body.Close()
		return nil, err
	}
	var decoder protocol.SDKFrameDecoder = protocol.NewSDKSSEDecoder(body)
	if client.config.Bedrock != nil {
		decoder = protocol.NewBedrockEventDecoder(body)
	}
	return &RawMessageStream{
		Headers: headers, RequestID: sdkRequestID(headers), OriginalHeaders: exchange.OriginalHeaders,
		ctx: ctx, body: body, cancel: cancel,
		decoder: decoder, diagnose: client.config.StreamDiagnostic,
		openAI: exchange.openAI,
	}, nil
}

func sdkRequestID(headers http.Header) *string {
	for name, values := range headers {
		if strings.EqualFold(name, "request-id") {
			// WHATWG Headers.get combines duplicate response header values.
			value := strings.Join(values, ", ")
			return &value
		}
	}
	return nil
}

func sdkRequestError(cause error) error {
	if IsOwnerWaitFailure(cause) {
		return cause
	}
	var existing *SDKStreamError
	if errors.As(cause, &existing) {
		return existing
	}
	class, kind := "", ""
	switch cause.Error() {
	case "Request was aborted.":
		class, kind = "APIUserAbortError", "request_abort"
	case "Request timed out.":
		class, kind = "APIConnectionTimeoutError", "request_timeout"
	case "Connection error.":
		class, kind = "APIConnectionError", "request_connection"
	default:
		return cause
	}
	encoded, _ := json.Marshal(cause.Error())
	return &SDKStreamError{Kind: kind, Class: class, Name: "Error", Message: cause.Error(), MessageJSON: encoded, Type: json.RawMessage(`null`)}
}

// AbortCause distinguishes the SDK's quiet iterator termination from success.
// The query-attempt policy owns whether this means user cancellation, watchdog
// expiry or retry; normal EOF itself must not silently decide that policy.
func (stream *RawMessageStream) AbortCause() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.aborted
}

func (stream *RawMessageStream) Next() (json.RawMessage, error) {
	for {
		stream.mu.Lock()
		closed := stream.closed
		owned := stream.owned
		stream.mu.Unlock()
		if owned {
			return nil, io.ErrClosedPipe
		}
		if closed {
			return nil, io.EOF
		}
		stream.mu.Lock()
		if stream.openAI != nil {
			reader, err := stream.openAI.reader(stream.ctx, stream.body)
			if err != nil {
				stream.mu.Unlock()
				return nil, err
			}
			stream.body = reader
			stream.decoder = protocol.NewSDKSSEDecoder(reader)
			stream.openAI = nil
		}
		stream.mu.Unlock()
		frame, err := stream.decoder.Next()
		if err != nil {
			if IsOwnerWaitFailure(err) {
				_ = stream.Close()
				return nil, err
			}
			if errors.Is(stream.ctx.Err(), context.Canceled) || errors.Is(stream.ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				stream.mu.Lock()
				stream.aborted = stream.ctx.Err()
				if stream.aborted == nil {
					stream.aborted = err
				}
				stream.mu.Unlock()
				err = io.EOF // SDK catches AbortError and returns its iterator.
			}
			_ = stream.Close()
			return nil, err
		}
		if frame.Event == "chunk" {
			if _, bedrock := stream.decoder.(*protocol.BedrockEventDecoder); !bedrock {
				continue
			}
		}
		switch frame.Event {
		case "completion", "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "chunk":
			var parseErr *SDKStreamError
			if !json.Valid([]byte(frame.Data)) {
				parseErr = sdkJSONSyntaxError(frame.Data)
			}
			if parseErr != nil {
				if stream.diagnose != nil {
					stream.diagnose("Could not parse message into JSON:", frame.Data)
					stream.diagnose("From chunk:", append([]string(nil), frame.Raw...))
				}
				_ = stream.Close()
				return nil, parseErr
			}
			// Preserve original doubles and UTF-16 escapes for business coercion.
			// JSON.stringify is an observation boundary, not an input transform.
			return json.RawMessage(frame.Data), nil
		case "error":
			_ = stream.Close()
			if stream.OriginalHeaders != nil {
				return nil, NewSDKResponseError(nil, frame.Data, stream.OriginalHeaders, false)
			}
			return nil, sdkAPIError(nil, frame.Data, stream.Headers, false)
		default:
			// Unknown names, data-only frames and ping do not even parse data.
		}
	}
}

func (stream *RawMessageStream) Close() error {
	stream.closeOnce.Do(func() {
		stream.mu.Lock()
		stream.closed = true
		body, binding := stream.body, stream.openAI
		stream.mu.Unlock()
		stream.cancel()
		if binding != nil && binding.bridge != nil {
			binding.bridge.Abort(context.Canceled)
		}
		stream.closeErr = body.Close()
		if binding != nil && binding.bridge != nil {
			binding.bridge.Dispose()
		}
	})
	return stream.closeErr
}
