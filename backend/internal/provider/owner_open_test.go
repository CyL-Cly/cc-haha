package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func ownerTestDriver(t *testing.T, scope *cooperative.Scope) *OwnerOpenIO {
	t.Helper()
	driver, err := NewOwnerOpenIO(func(_ OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
		return scope.AwaitBytes(cooperative.NetworkOpen, work)
	})
	if err != nil {
		t.Error(err)
	}
	return driver
}

func TestOwnerOpenBodyOutlivesTemporaryWaitAndHeaderTimer(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver := ownerTestDriver(t, scope)
		finalized := 0
		client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: 100 * time.Millisecond, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver, FinalizeHeaders: func(_ string, h http.Header) (http.Header, error) { finalized++; return h, nil }})
		stream, err := client.OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
		if err != nil {
			return err
		}
		defer stream.Close()
		if finalized != 1 || driver.LeaseCount() != 1 {
			return fmt.Errorf("owner finalization/lease %d/%d", finalized, driver.LeaseCount())
		}
		// Headers have already arrived. This actual timer cannot own the body.
		time.Sleep(150 * time.Millisecond)
		close(release)
		part, err := stream.Next()
		if err != nil || string(part) != `{"type":"message_stop"}` || stream.AbortCause() != nil {
			return fmt.Errorf("body incorrectly canceled: %s %v %v", part, err, stream.AbortCause())
		}
		_ = stream.Close()
		if driver.LeaseCount() != 0 {
			return errors.New("adopted body leaked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestImmutableSendPlanDoesNotBorrowOriginalHeadersOrBody(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/a?beta=true", strings.NewReader("original"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Original", "one")
	plan, err := buildSendPlan(request, &RawHTTP1Exchange{})
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Original", "changed")
	request.URL.Path = "/changed"
	if plan.headers.Get("X-Original") != "one" || string(plan.body) != "original" || !strings.HasSuffix(plan.endpoint, "/a?beta=true") {
		t.Fatal("plan borrowed mutable request")
	}
}

func TestCompletionDiscardClosesReservedLeaseBeforeOwnerReturns(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	discard := errors.New("owner completion discarded")
	driver, err := NewOwnerOpenIO(func(_ OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
		_, err := work(context.Background())
		if err != nil {
			return nil, err
		}
		return nil, discard
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("original"))
	_, err = driver.open(request, &RawHTTP1Exchange{}, nil)
	if !errors.Is(err, discard) || driver.LeaseCount() != 0 {
		t.Fatalf("discard=%v leases=%d", err, driver.LeaseCount())
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("discarded response still owns socket")
	}
}

func TestErrorBodyParkUsesSameLeaseAndClosesOnCallerCancellation(t *testing.T) {
	header := make(chan struct{})
	parked := make(chan struct{})
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.(http.Flusher).Flush()
		close(header)
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := cooperative.New()
	defer owner.Close()
	go func() { <-header; <-parked; cancel() }()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, err := NewOwnerOpenIO(func(kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			if kind == OwnerErrorBody {
				close(parked)
			}
			return scope.AwaitBytes(cooperative.NetworkOpen, work)
		})
		if err != nil {
			return err
		}
		client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver})
		_, failure := client.OpenMessagesStream(caller, &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
		// This is a lease/resource assertion, not proof of Bun's read-error text.
		var sdk *SDKStreamError
		if !errors.As(failure, &sdk) || sdk.Status == nil || *sdk.Status != 500 {
			return fmt.Errorf("error body ownership: %v", failure)
		}
		if driver.LeaseCount() != 0 {
			return errors.New("failed body lease leaked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("failed body still owns socket")
	}
}

func TestOwnerOpenResponseProjectionKeepsRawDuplicateHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Uncommon", "first")
		w.Header().Add("X-Uncommon", "last")
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.Header().Set("Request-Id", "")
		w.WriteHeader(200)
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver := ownerTestDriver(t, scope)
		client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver})
		stream, err := client.OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
		if err != nil {
			return err
		}
		defer stream.Close()
		value, present, _ := stream.OriginalHeaders.Get("x-uncommon")
		if !present || value != "last" {
			return fmt.Errorf("headers %q", value)
		}
		if stream.RequestID != nil || len(stream.OriginalHeaders.SetCookies()) != 2 {
			return errors.New("native empty-header/cookie projection lost")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOwnerOpenRejectsForeignErrorBodyAndMissingAwait(t *testing.T) {
	if _, err := NewOwnerOpenIO(nil); err == nil {
		t.Fatal("implicit await")
	}
	driver, _ := NewOwnerOpenIO(func(_ OwnerIOWait, _ func(context.Context) ([]byte, error)) ([]byte, error) {
		t.Error("foreign body reached worker")
		return nil, nil
	})
	if _, err := driver.errorBody(io.NopCloser(strings.NewReader("not owned"))); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
}
