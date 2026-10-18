package main

import (
	"net/http"
	"sync/atomic"
	"testing"
)

// The real public CLI init sends an unauthenticated HEAD before normal model
// work. Preserve each existing model handler/assertion while recording that
// separate physical transport effect at the peer, rather than counting it as
// a model invocation or fabricating a model response for an empty HEAD body.
func nativeCLIModelHTTPHandler(t *testing.T, model http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	var heads atomic.Int32
	t.Cleanup(func() {
		t.Logf("actual CLI init HEAD transport requests=%d (separate from unchanged model counters)", heads.Load())
	})
	return func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			heads.Add(1)
			if request.Header.Get("Authorization") != "" || request.Header.Get("X-Api-Key") != "" || request.Header.Get("User-Agent") != "Bun/1.3.14" {
				t.Error("init HEAD inherited model authentication or changed Source fetch identity", request.Header)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		model(w, request)
	}
}
