package autoquestion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testTimer struct {
	mu       sync.Mutex
	stopped  bool
	fn       func()
	duration time.Duration
}

func (t *testTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	old := !t.stopped
	t.stopped = true
	return old
}
func (t *testTimer) fire() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	fn := t.fn
	t.mu.Unlock()
	fn()
}
func TestAutomaticQuestionCancellationRefreshAndIdentityUseActualHTTP(t *testing.T) {
	for _, kind := range []string{"valid", "activity", "removed", "disabled", "provider_changed", "missing_context", "missing_provider", "invalid_model_answer", "replaced", "cancel_inflight"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var calls atomic.Int32
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				started <- struct{}{}
				if kind == "cancel_inflight" {
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				text := `{"answers":[{"questionIndex":0,"optionLabels":["JSON"]}]}`
				if kind == "invalid_model_answer" {
					text = `{"answers":[{"questionIndex":0,"optionLabels":["Invented"]}]}`
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}})
			}))
			defer httpServer.Close()
			var mu sync.Mutex
			settings := []byte(`{"enabled":true,"timeoutMinutes":5}`)
			now := time.Unix(1000, 0)
			timers := make(chan *testTimer, 8)
			resolved := make(chan json.RawMessage, 2)
			id := "fixture-provider"
			service, e := New(Options{ReadSettings: func(context.Context) (json.RawMessage, error) {
				mu.Lock()
				defer mu.Unlock()
				return append(json.RawMessage(nil), settings...), nil
			}, Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }, AfterFunc: func(d time.Duration, f func()) Timer {
				timer := &testTimer{fn: f, duration: d}
				timers <- timer
				return timer
			}, Decider: Decider{Resolve: func(context.Context, *string, string) (*Target, error) {
				return &Target{URL: httpServer.URL, Model: "actual-small", Headers: http.Header{"Content-Type": []string{"application/json"}}}, nil
			}}})
			if e != nil {
				t.Fatal(e)
			}
			defer service.Close(ctx)
			binding := Binding{ProviderID: &id, ProviderFingerprint: []byte(`{"baseUrl":"original","apiKey":"fake-original"}`), ReadProviderFingerprint: func(context.Context, string) (json.RawMessage, error) {
				if kind == "provider_changed" {
					return []byte(`{"baseUrl":"changed","apiKey":"fake-original"}`), nil
				}
				return []byte(`{"baseUrl":"original","apiKey":"fake-original"}`), nil
			}, ReadContext: func(context.Context, string) (json.RawMessage, error) {
				if kind == "missing_context" {
					return encode(""), nil
				}
				return encode("user: Prefer JSON."), nil
			}, Resolve: func(_ context.Context, _ string, input json.RawMessage) (bool, error) {
				resolved <- input
				return true, nil
			}}
			if kind == "missing_provider" {
				binding.ProviderID = nil
			}
			service.Bind("session", binding)
			input := object(questionInput(false))
			input.Set("metadata", []byte(`{"keep":true,"autoAnswered":false}`))
			service.Track("session", Request{ID: "request", ToolName: "AskUserQuestion", Input: encode(input)})
			var timer *testTimer
			select {
			case timer = <-timers:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if timer.duration != 5*time.Minute {
				t.Fatal(timer.duration)
			}
			if kind == "activity" {
				service.CancelActivity("session", "request")
				service.Refresh()
				timer.fire()
			} else if kind == "removed" {
				service.Remove("session", "request")
				timer.fire()
			} else if kind == "disabled" {
				mu.Lock()
				settings = []byte(`{"enabled":false,"timeoutMinutes":1}`)
				mu.Unlock()
				timer.fire()
			} else if kind == "replaced" {
				service.Bind("session", binding)
				timer.fire()
			} else {
				// Refresh uses the original creation time rather than restarting a new
				// full timeout. Settings changed after two minutes means zero wait.
				mu.Lock()
				now = now.Add(2 * time.Minute)
				settings = []byte(`{"enabled":true,"timeoutMinutes":1}`)
				mu.Unlock()
				service.Refresh()
				timer.fire()
				select {
				case timer = <-timers:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if timer.duration != 0 {
					t.Fatal(timer.duration)
				}
				if kind == "cancel_inflight" {
					go timer.fire()
					select {
					case <-started:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					service.CancelActivity("session", "request")
					close(release)
				} else {
					timer.fire()
				}
			}
			if e = service.Close(ctx); e != nil {
				t.Fatal(e)
			}
			want := int32(0)
			if kind == "valid" || kind == "invalid_model_answer" || kind == "cancel_inflight" {
				want = 1
			}
			if calls.Load() != want {
				t.Fatal("physical calls", calls.Load(), want)
			}
			select {
			case input := <-resolved:
				if kind != "valid" {
					t.Fatal("unexpected approval", kind, string(input))
				}
				if object(input).Get("answers") == nil || string(object(object(input).Get("metadata")).Get("autoAnswered")) != "true" || object(object(input).Get("metadata")).Get("keep") == nil {
					t.Fatal(string(input))
				}
			default:
				if kind == "valid" {
					t.Fatal("did not resolve")
				}
			}
		})
	}
}
