package provider

import (
	"errors"
	"io"
	"syscall"
)

// A completed fetch whose body socket is cut has Bun's native Error identity,
// rather than an SDK APIError. Retain the physical cause for retry inspection.
type NativeStreamReadError struct{ cause error }

func (e *NativeStreamReadError) Error() string {
	return "The socket connection was closed unexpectedly. For more information, pass `verbose: true` in the second argument to fetch()"
}
func (e *NativeStreamReadError) Code() string  { return "ECONNRESET" }
func (e *NativeStreamReadError) Unwrap() error { return e.cause }

func nativeBodyReadError(failure error) error {
	if IsOwnerWaitFailure(failure) {
		return failure
	}
	var codec *NativeCompressionReadError
	if errors.As(failure, &codec) {
		return failure
	}
	if errors.Is(failure, io.ErrUnexpectedEOF) || errors.Is(failure, syscall.ECONNRESET) || errors.Is(failure, syscall.ECONNABORTED) || errors.Is(failure, syscall.EPIPE) {
		return &NativeStreamReadError{failure}
	}
	return failure
}
