package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBunFetchHeaderProfileIsSeparateFromConstructedHeaders(t *testing.T) {
	header, err := sdkResponseHeaders(http.Header{
		"Request-Id": {"first", "second", " \t"}, "Content-Type": {" application/json ", "text/plain", ""},
		"Set-Cookie": {" first=a ", "second=b", ""}, "X-Future": {"one", "two"}, "X-Empty": {"", " \t"}, "Date": {" Thu, 01 Jan 1970 00:00:00 GMT "},
	})
	if err != nil || !reflect.DeepEqual(header, http.Header{"Request-Id": {"second"}, "Content-Type": {"application/json, text/plain"}, "Set-Cookie": {"first=a", "second=b"}, "X-Future": {"two"}, "Date": {"Thu, 01 Jan 1970 00:00:00 GMT"}}) {
		t.Fatal(header, err)
	}
	if got := sdkRequestID(http.Header{"Request-Id": {"first", "second"}}); got == nil || *got != "first, second" {
		t.Fatal("constructed WHATWG Headers read incorrectly normalized as fetch", got)
	}
	if got := sdkRequestID(http.Header{"request-id": {""}}); got == nil || *got != "" {
		t.Fatal("constructed empty header lost", got)
	}
	if got := sdkRequestID(header); got == nil || *got != "second" {
		t.Fatal(got)
	}
	if got, err := sdkResponseHeaders(nil); err != nil || got == nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestUnprovenDuplicateHeadersFailWithoutInventingSourceMetadata(t *testing.T) {
	for _, raw := range []http.Header{{"X-Unproven": {"first", "second"}}, {"Request-Id": {"first"}, "request-id": {"second"}}} {
		if _, err := sdkResponseHeaders(raw); !errors.Is(err, ErrResponseHeaderProfile) {
			t.Fatal(raw, err)
		}
	}
	body := &observedStreamBody{Reader: strings.NewReader("event: completion\ndata: 1\n\n")}
	calls := 0
	client := NewAnthropic(AnthropicConfig{APIKey: "fixture", Timeout: time.Second, HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: body, Header: http.Header{"X-Unproven": {"first", "second"}}}, nil
	})}})
	if _, err := client.OpenMessagesStream(context.Background(), preparedStream()); !errors.Is(err, ErrResponseHeaderProfile) || !body.closed || calls != 1 {
		t.Fatal(err, body.closed, calls)
	}
	if got := sdkRequestID(http.Header{}); got != nil {
		t.Fatal(got)
	}
	// A Go Close flag can come from HTTP/1.0 or an unbounded body. It cannot
	// prove the spelling/value of a Connection header removed by net/http.
	response := &http.Response{Close: true, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}
	header, err := sdkResponseHeaders(response.Header)
	if err != nil || header.Get("Connection") != "" {
		t.Fatal("invented original header from connection state", header, err)
	}
}
