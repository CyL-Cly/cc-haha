package retrypolicy

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

type nativeCause struct {
	fields  protocol.JSONObject
	failure error
}

func (cause *nativeCause) Error() string { return "diagnostic is non-enumerable" }
func (cause *nativeCause) EnumerableErrorJSON() (protocol.JSONObject, error) {
	return cause.fields, cause.failure
}

func noticeWire(t *testing.T, notice RetryNotice) string {
	t.Helper()
	object, err := notice.Envelope()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := object.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestNoticeKeepsOriginalSDKPayloadAndOmitsUndefinedMetadata(t *testing.T) {
	failure := statusFailure(500)
	failure.Payload = json.RawMessage(`{"9":9,"2":2,"text":"<>&\ud800","value":9007199254740993,"negative":-0}`)
	notice := RetryNotice{Error: failure, MaxRetries: math.Inf(1), RetryInMillis: math.NaN(), UUID: "<>&"}
	wire := noticeWire(t, notice)
	for _, expected := range []string{`"error":{"status":500,"error":{"2":2,"9":9,"text":"<>&\ud800","value":9007199254740992,"negative":0}}`, `"retryInMs":null`, `"maxRetries":null`, `"uuid":"<>&"`} {
		if !strings.Contains(wire, expected) {
			t.Fatalf("missing %s in %s", expected, wire)
		}
	}
	for _, forbidden := range []string{`"headers"`, `"requestID"`, `"cause"`, `"message"`} {
		if strings.Contains(wire, forbidden) {
			t.Fatalf("manufactured %s in %s", forbidden, wire)
		}
	}
	failure.Headers = make(http.Header)
	failure.Payload = json.RawMessage(`null`)
	wire = noticeWire(t, notice)
	if !strings.Contains(wire, `"headers":{},"requestID":null,"error":null`) {
		t.Fatalf("lost explicit null: %s", wire)
	}
}

func TestNoticeRetainsOriginalNativeCausePropertiesAtBothOwnershipSites(t *testing.T) {
	cause := &nativeCause{fields: protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: json.RawMessage(`"ECONNRESET"`)}, protocol.JSONField{Name: "cause", Value: json.RawMessage(`{"code":"EPIPE"}`)})}
	failure := &provider.SDKStreamError{Class: "APIConnectionError", Cause: cause, Message: "Connection error."}
	wire := noticeWire(t, RetryNotice{Error: failure, Cause: cause})
	if strings.Count(wire, `"cause":{"code":"ECONNRESET","cause":{"code":"EPIPE"}}`) != 2 || strings.Contains(wire, "diagnostic") || strings.Contains(wire, "Connection error.") {
		t.Fatalf("cause ownership changed: %s", wire)
	}
	cause.failure = errors.New("worker failed to retain metadata")
	if _, err := (RetryNotice{Error: failure, Cause: cause}).Envelope(); !errors.Is(err, cause.failure) {
		t.Fatalf("hidden boundary error: %v", err)
	}
	cause.failure = nil
	cause.fields.Set("corrupt", json.RawMessage(`{`))
	if _, err := (RetryNotice{Error: failure}).Envelope(); err == nil {
		t.Fatal("accepted corrupt cause properties")
	}
}

func TestNoticeStopsAtUnportedMetadataInsteadOfInventingWireFields(t *testing.T) {
	for _, failure := range []*provider.SDKStreamError{
		nil, {Class: "UnknownSDKError"},
		{Class: "APIError", Payload: json.RawMessage(`{`)},
		{Class: "APIError", Cause: errors.New("unported transport")},
		{Class: "APIError", Headers: http.Header{"X-A": {"a"}, "X-B": {"b"}}},
	} {
		if _, err := (RetryNotice{Error: failure}).Envelope(); err == nil {
			t.Fatalf("accepted undeclared metadata %#v", failure)
		}
	}
}

func TestNoticeSupportsSourceSingleHeaderSerializationAndCookieArray(t *testing.T) {
	for _, headers := range []http.Header{{"Retry-After": {"19"}}, {"Set-Cookie": {"a=1", "b=2"}}} {
		failure := statusFailure(500)
		failure.Headers = headers
		wire := noticeWire(t, RetryNotice{Error: failure})
		if strings.HasPrefix(headers.Get("Retry-After"), "19") {
			if !strings.Contains(wire, `"headers":{"retry-after":"19"}`) {
				t.Fatal(wire)
			}
		} else if !strings.Contains(wire, `"headers":{"set-cookie":["a=1","b=2"]}`) {
			t.Fatal(wire)
		}
	}
}

func TestNoticeUsesOriginalBunHeaderVectorsAtBothCauseSites(t *testing.T) {
	headers, err := provider.NewFetchHeaders([]provider.HeaderPair{{Name: "X-z", Value: "first"}, {Name: "Content-Type", Value: "application/json"}, {Name: "Set-Cookie", Value: "a=1"}, {Name: "2", Value: "two"}, {Name: "Connection", Value: "keep-alive, CLOSE"}, {Name: "Set-Cookie", Value: "b=2"}, {Name: "X-z", Value: "last"}, {Name: "1", Value: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	status := 500
	var failure *provider.SDKStreamError
	if !errors.As(provider.NewSDKResponseError(&status, `{"message":"fault"}`, headers, true), &failure) {
		t.Fatal("SDK failure missing")
	}
	// A conflicting policy map cannot manufacture order. Mutating the shared
	// original Headers must remain visible at both SDK ownership sites.
	failure.Headers = http.Header{"X-fabricated": {"wrong"}}
	_ = headers.Set("connection", "changed")
	wire := noticeWire(t, RetryNotice{Error: failure, Cause: failure})
	expected := `"headers":{"1":"one","2":"two","content-type":"application/json","connection":"changed","set-cookie":["a=1","b=2"],"x-z":"last"}`
	if strings.Count(wire, expected) != 2 || strings.Contains(wire, "fabricated") {
		t.Fatal(wire)
	}
	failure.Headers = nil
	if !strings.Contains(noticeWire(t, RetryNotice{Error: failure}), expected) {
		t.Fatal("originals incorrectly require a map")
	}
}

func TestFastExpiryNotificationCanReenterOrRejectWithoutDeadlock(t *testing.T) {
	if _, err := ReadFastState(nil, time.Now(), true, nil); err == nil {
		t.Fatal("missing state accepted")
	}
	reset := 1.0
	state := &FastState{CooldownUntil: &reset, CooldownReason: "rate_limit"}
	count := 0
	snapshot, err := ReadFastState(state, time.UnixMilli(2), true, func(FastEffect) error {
		count++
		nested, err := ReadFastState(state, time.UnixMilli(2), true, nil)
		if err != nil || nested.Status != "active" {
			t.Fatalf("reentrant state: %#v %v", nested, err)
		}
		return nil
	})
	if err != nil || snapshot.Status != "active" || count != 1 {
		t.Fatalf("snapshot=%#v error=%v count=%d", snapshot, err, count)
	}
	state.setCooldown(1, "overloaded")
	if _, err := ReadFastState(state, time.UnixMilli(2), true, nil); err == nil {
		t.Fatal("missing notification accepted")
	}
	state.setCooldown(1, "overloaded")
	boundary := errors.New("notification failed")
	if _, err := ReadFastState(state, time.UnixMilli(2), true, func(FastEffect) error { return boundary }); !errors.Is(err, boundary) {
		t.Fatalf("lost notification failure: %v", err)
	}
	if (&CannotRetryError{Original: (*provider.SDKStreamError)(nil)}).Error() != "undefined" {
		t.Fatal("typed nil error promoted")
	}
}
