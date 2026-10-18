package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualPublicRunCommandPreconnectHeadDoesNotHoldNextHarnessPost(t *testing.T) {
	var heads, posts atomic.Int32
	headStarted, postStarted := make(chan struct{}), make(chan struct{})
	var headOnce, postOnce sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			heads.Add(1)
			if request.URL.Path != "/" || request.Header.Get("Authorization") != "" || request.Header.Get("X-Api-Key") != "" || request.Header.Get("User-Agent") != "Bun/1.3.14" {
				t.Error("preconnect did not use original unauthenticated fetch", request.URL, request.Header)
			}
			headOnce.Do(func() { close(headStarted) })
			select {
			case <-postStarted:
			case <-request.Context().Done():
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
			t.Error("unexpected public request", request.Method, request.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		posts.Add(1)
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), "PRECONNECT_PUBLIC_HUMAN") {
			t.Error("next request did not use the shared Harness input", string(body))
		}
		postOnce.Do(func() { close(postStarted) })
		select {
		case <-headStarted:
		case <-time.After(time.Second):
		}
		response(w, "preconnect-public-response", "", nil, "PRECONNECT_PUBLIC_RESPONSE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = io.Discard
	command, parsed, err := ParseCommand([]string{"--bare", "--print", "--model", "fixture-model", "--tools", "", "--system-prompt", "private preconnect public prompt", "--thinking", "disabled", "PRECONNECT_PUBLIC_HUMAN"}, o.Version, o.WorkDir, o.Native.Temp, o.Environment)
	if err != nil || parsed.Parsed == nil {
		t.Fatal(err, parsed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || !strings.Contains(output.String(), "PRECONNECT_PUBLIC_RESPONSE") {
		t.Fatal("actual public next model result", code, err, output.String())
	}
	if heads.Load() != 1 || posts.Load() != 1 {
		t.Fatalf("original normal CLI preconnect consumer: HEAD=%d modelPOST=%d", heads.Load(), posts.Load())
	}
}

func TestActualNativeSessionCloseCancelsHeldPreconnectAndModelHTTP(t *testing.T) {
	headStarted, headCancelled := make(chan struct{}), make(chan struct{})
	postStarted, postCancelled := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodHead:
			close(headStarted)
			<-request.Context().Done()
			close(headCancelled)
		case http.MethodPost:
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), "PRECONNECT_HELD_MODEL_HUMAN") {
				t.Error("actual foreground Harness input was not sent", string(body))
			}
			close(postStarted)
			<-request.Context().Done()
			close(postCancelled)
		default:
			t.Error("unexpected init/model method", request.Method)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Tools = []string{""}
	session, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := session.process.BeginNativeAPIPreconnect(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-headStarted:
	case <-ctx.Done():
		t.Fatal("actual HEAD did not start", ctx.Err())
	}
	turn := make(chan error, 1)
	go func() { turn <- session.Submit(ctx, tui.Input{Text: "PRECONNECT_HELD_MODEL_HUMAN"}, nil) }()
	select {
	case <-postStarted:
	case <-ctx.Done():
		t.Fatal("held preconnect blocked ordinary model", ctx.Err())
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal("global Session close failed to join init/model transports", err)
	}
	for label, signal := range map[string]<-chan struct{}{"HEAD": headCancelled, "model": postCancelled} {
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatal("global close left physical socket active", label, ctx.Err())
		}
	}
	select {
	case <-turn:
	case <-ctx.Done():
		t.Fatal("foreground submit survived global close", ctx.Err())
	}
}

func TestActualPublicPreconnectUsesSettingsEnvironmentBeforeNextModel(t *testing.T) {
	var heads, posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			heads.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		if request.Method != http.MethodPost || !strings.Contains(string(body), "PRECONNECT_SETTINGS_HUMAN") {
			t.Error("settings environment did not reach actual model", request.Method, string(body))
		}
		posts.Add(1)
		response(w, "preconnect-settings", "", nil, "PRECONNECT_SETTINGS_RESPONSE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, "http://127.0.0.1:1/private-unselected-launch-base")
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), raw(map[string]any{"env": map[string]string{"ANTHROPIC_BASE_URL": peer.URL}}), 0600); err != nil {
		t.Fatal(err)
	}
	command, _, err := ParseCommand([]string{"-p", "--model", "fixture-model", "--tools", "", "--system-prompt", "private configured preconnect", "--thinking", "disabled", "PRECONNECT_SETTINGS_HUMAN"}, o.Version, o.WorkDir, o.Native.Temp, o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || !strings.Contains(output.String(), "PRECONNECT_SETTINGS_RESPONSE") || heads.Load() != 1 || posts.Load() != 1 {
		t.Fatal("actual settings→init HEAD→same Harness POST", code, err, output.String(), heads.Load(), posts.Load())
	}
}

func TestPrivatePreconnectPrimitiveRejectsExternalRedirectBeforeTransport(t *testing.T) {
	var loopbackCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		loopbackCalls.Add(1)
		w.Header().Set("Location", "https://unowned-preconnect.invalid/forbidden")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	request, _ := http.NewRequest(http.MethodHead, peer.URL, nil)
	response, err := o.Native.PreconnectHTTP(request, &http.Client{})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "redirect escapes loopback") || loopbackCalls.Load() != 1 {
		t.Fatal("external redirect reached an unowned transport", err, loopbackCalls.Load())
	}
	for _, endpoint := range []string{"https://api.anthropic.com", "http://localhost:1/", "file:///tmp/forbidden", "http://user@127.0.0.1:1/"} {
		request, _ := http.NewRequest(http.MethodHead, endpoint, nil)
		if _, err := o.Native.PreconnectHTTP(request, &http.Client{}); err == nil {
			t.Fatal("unowned external preconnect admitted", endpoint)
		}
	}
	request, _ = http.NewRequest(http.MethodPost, peer.URL, nil)
	if _, err := o.Native.PreconnectHTTP(request, &http.Client{}); err == nil || loopbackCalls.Load() != 1 {
		t.Fatal("private preconnect primitive admitted a model method", err, loopbackCalls.Load())
	}
}
