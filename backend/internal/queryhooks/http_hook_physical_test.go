package queryhooks

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPHookPhysicalPostPolicyHeadersNoRedirectAndCancellation(t *testing.T) {
	var calls atomic.Int32
	waiting := make(chan struct{})
	serverCleanup := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/wait" {
			_, _ = io.ReadAll(r.Body)
			close(waiting)
			select {
			case <-r.Context().Done():
			case <-serverCleanup:
			}
			return
		}
		b, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || string(b) != `{"actual":"input"}` || r.Header.Get("X-Fake") != "fake-value" || r.Header.Get("X-Missing") != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("physical Hook request", r.Method, string(b), r.Header)
		}
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/must-not-follow")
			w.WriteHeader(302)
			return
		}
		w.WriteHeader(201)
		io.WriteString(w, `{"decision":"block","reason":"actual hook response"}`)
	}))
	defer peer.Close()
	defer close(serverCleanup)
	hook := protocol.NewJSONObject(protocol.JSONField{Name: "url", Value: commandJSON(peer.URL + "/hook")}, protocol.JSONField{Name: "headers", Value: json.RawMessage(`{"X-Fake":"$TOKEN","X-Missing":"$PRIVATE"}`)}, protocol.JSONField{Name: "allowedEnvVars", Value: json.RawMessage(`["TOKEN","PRIVATE"]`)})
	allow := []string{"TOKEN"}
	r := HTTPHookRequest{Hook: hook, Input: `{"actual":"input"}`, Environment: map[string]string{"TOKEN": "fake\r\n-value", "PRIVATE": "must-not-leak"}, Policy: HTTPHookPolicy{AllowedEnvironment: &allow}}
	out, e := RunHTTPHook(context.Background(), r)
	if e != nil || !out.OK || out.StatusCode == nil || *out.StatusCode != 201 || !strings.Contains(out.Body, "actual hook response") {
		t.Fatal(out, e)
	}
	empty := []string{}
	r.Policy.AllowedURLs = &empty
	out, e = RunHTTPHook(context.Background(), r)
	if e != nil || out.OK || !strings.Contains(out.Error, "allowedHttpHookUrls") || calls.Load() != 1 {
		t.Fatal("deny before any I/O", out, e, calls.Load())
	}
	r.Policy.AllowedURLs = nil
	r.Hook.Set("url", commandJSON(peer.URL+"/redirect"))
	out, e = RunHTTPHook(context.Background(), r)
	if e != nil || out.OK || out.StatusCode == nil || *out.StatusCode != 302 || calls.Load() != 2 {
		t.Fatal("redirect followed", out, e, calls.Load())
	}
	r.Hook.Set("url", commandJSON(peer.URL+"/wait"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan HTTPHookOutput, 1)
	go func() {
		out, e := RunHTTPHook(ctx, r)
		if e != nil {
			t.Error(e)
		}
		done <- out
	}()
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("physical server not entered")
	}
	cancel()
	select {
	case out = <-done:
		if !out.Aborted {
			t.Fatal("not aborted", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP cancellation did not join")
	}
}
