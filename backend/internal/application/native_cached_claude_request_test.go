package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestCachedSubscriberActualNativePrintSameHarnessHTTPAuthAndSharedTLS(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fake-usage-token" || r.Header.Get("X-Api-Key") != "" || len(r.Header.Values("X-Api-Key")) != 0 {
			t.Error("cached actual identity not used", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("actual modelbody absent")
		}
		response(w, "cached-native", "", nil, "CACHED_NATIVE_MODEL_DONE")
	}))
	defer peer.Close()
	proxy, ca := terminalUsagePrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected account network %s", r.URL)
		w.WriteHeader(404)
	}))
	o := terminalUsageFixtureOptions(t, peer.URL, proxy, ca)
	var observed []error
	o.ObserveModelFailure = func(err error) {
		observed = append(observed, err)
		t.Logf("actualOpenFailure %T %v", err, err)
		if exhausted, ok := err.(*retrypolicy.CannotRetryError); ok {
			t.Logf("actualOriginalFailure %T %v", exhausted.Original, exhausted.Original)
		}
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	err = s.Submit(context.Background(), tui.Input{Text: "CACHED_NATIVE_ACTUAL_QUERY"}, nil)
	if err != nil || calls.Load() != 1 || len(observed) != 0 {
		t.Fatal("cached real request failed", err, calls.Load(), observed)
	}
}
