package provider

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// This exercises the bounded original-row projection seam. It does not assert
// native Bun fetch handling of malformed response syntax or admission ties.
func TestNativeOwnerOriginalHeaderProjectionRejectsUnportedShapes(t *testing.T) {
	cases := []struct {
		name, raw string
		status    int
	}{
		{"incomplete", "HTTP/1.1 200 OK\r\n", 200},
		{"missing-colon", "HTTP/1.1 200 OK\r\nOriginalValue\r\n\r\n", 200},
		{"empty-row", "HTTP/1.1 200 OK\r\n\r\nX: after\r\n\r\n", 200},
		{"folded", "HTTP/1.1 200 OK\r\n\told\r\n\r\n", 200},
		{"header-name", "HTTP/1.1 200 OK\r\nBad Name: value\r\n\r\n", 200},
		{"header-value", "HTTP/1.1 200 OK\r\nX-Original: \x00\r\n\r\n", 200},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if _, err := nativeOwnerHeaders([]byte(item.raw), item.status); err == nil {
				t.Fatal("original malformed/unsupported header accepted")
			}
		})
	}
	headers, err := nativeOwnerHeaders([]byte("HTTP/1.1 200 OK\r\nx-original: first\r\nx-original: second\r\ncontent-encoding: identity\r\n\r\n"), 200)
	if err != nil {
		t.Fatal(err)
	}
	listHeaders, listError := nativeOwnerHeaders([]byte("HTTP/1.1 200 OK\r\ncontent-encoding: identity, br\r\n\r\n"), 200)
	if listError != nil {
		t.Fatal(listError)
	}
	encoding, _, _ := listHeaders.Get("content-encoding")
	if encoding != "identity, br" {
		t.Fatal("original encoding header changed", encoding)
	}
	value, _, _ := headers.Get("x-original")
	if value != "second" {
		t.Fatal("unknown native header duplicate ordering changed", value)
	}
	if _, err = InspectNativeOwnerBody(io.NopCloser(strings.NewReader("other"))); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal("foreign body lease accepted", err)
	}
}

func TestNativeOwnerAdministrativeMessageCannotMasqueradeAsSDKTimeout(t *testing.T) {
	for _, message := range []string{"Request timed out.", "Request was aborted.", "Connection error."} {
		original := &ownerWaitFailure{errors.New(message)}
		if got := sdkRequestError(original); got != original || !IsOwnerWaitFailure(got) {
			t.Fatal("scheduler feedback normalized as SDK network error", got)
		}
	}
}
