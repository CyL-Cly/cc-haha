package autoquestion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestAutomaticQuestionActualSourceSchedulesCancelsAndAuthorizesOnce(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"answers\":[{\"questionIndex\":0,\"optionLabels\":[\"JSON\"]}]}"}]}`))
	}))
	defer provider.Close()
	kinds := []string{"valid", "activity", "removed", "disabled", "provider_changed", "missing_context"}
	cases := []map[string]any{}
	for _, kind := range kinds {
		cases = append(cases, map[string]any{"kind": kind, "input": questionInput(false), "provider": map[string]any{"presetId": "custom", "name": "Fixture", "apiKey": "fake-key", "authStrategy": "api_key", "baseUrl": provider.URL, "apiFormat": "anthropic", "models": map[string]string{"main": "large", "haiku": "small", "sonnet": "large", "opus": "large"}}})
	}
	out := actualSource(t, map[string]any{"cases": []any{}, "lifecycle": cases, "loopbackFetchOrigin": provider.URL})
	var rows []json.RawMessage
	_ = json.Unmarshal(object(out).Get("lifecycle"), &rows)
	if len(rows) != len(kinds) {
		t.Fatal(string(out))
	}
	for i, kind := range kinds {
		var mu sync.Mutex
		now := time.Unix(1000, 0)
		settings := []byte(`{"enabled":true,"timeoutMinutes":1}`)
		timers := make(chan *testTimer, 4)
		inputs := []json.RawMessage{}
		service, e := New(Options{ReadSettings: func(context.Context) (json.RawMessage, error) {
			mu.Lock()
			defer mu.Unlock()
			return append(json.RawMessage(nil), settings...), nil
		}, Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }, AfterFunc: func(d time.Duration, f func()) Timer {
			timer := &testTimer{fn: f, duration: d}
			timers <- timer
			return timer
		}, Decider: Decider{Resolve: func(context.Context, *string, string) (*Target, error) {
			return &Target{Model: "small", URL: provider.URL, Headers: http.Header{}}, nil
		}}})
		if e != nil {
			t.Fatal(e)
		}
		id := "fixture"
		service.Bind("session", Binding{ProviderID: &id, ProviderFingerprint: []byte(`{"key":"original"}`), ReadProviderFingerprint: func(context.Context, string) (json.RawMessage, error) {
			if kind == "provider_changed" {
				return []byte(`{"key":"changed"}`), nil
			}
			return []byte(`{"key":"original"}`), nil
		}, ReadContext: func(context.Context, string) (json.RawMessage, error) {
			if kind == "missing_context" {
				return encode(""), nil
			}
			return encode("user: Prefer JSON."), nil
		}, Resolve: func(_ context.Context, _ string, input json.RawMessage) (bool, error) {
			inputs = append(inputs, input)
			return true, nil
		}})
		service.Track("session", Request{ID: "request", ToolName: "AskUserQuestion", Input: questionInput(false)})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var timer *testTimer
		select {
		case timer = <-timers:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		mu.Lock()
		now = now.Add(2 * time.Minute)
		if kind == "disabled" {
			settings = []byte(`{"enabled":false,"timeoutMinutes":1}`)
		}
		mu.Unlock()
		if kind == "activity" {
			service.CancelActivity("session", "request")
		}
		if kind == "removed" {
			service.Remove("session", "request")
		}
		service.Refresh()
		timer.fire()
		if kind == "valid" || kind == "provider_changed" || kind == "missing_context" {
			select {
			case timer = <-timers:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if timer.duration != 0 {
				t.Fatal(timer.duration)
			}
			timer.fire()
		}
		service.mu.Lock()
		p := service.sessions["session"].requests["request"]
		pending := p != nil
		cancelled := p != nil && p.cancelled
		service.mu.Unlock()
		if e = service.Close(ctx); e != nil {
			t.Fatal(e)
		}
		cancel()
		source := object(rows[i])
		var sent []json.RawMessage
		_ = json.Unmarshal(source.Get("sent"), &sent)
		sourceInputs := []json.RawMessage{}
		for _, raw := range sent {
			sourceInputs = append(sourceInputs, object(object(object(raw).Get("response")).Get("response")).Get("updatedInput"))
		}
		actual := encode(map[string]any{"inputs": inputs, "pending": pending, "cancelled": cancelled, "autoResolved": len(inputs) > 0})
		expected := encode(map[string]any{"inputs": sourceInputs, "pending": source.Get("pending"), "cancelled": source.Get("cancelled"), "autoResolved": source.Get("autoResolved")})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(expected, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s %s\n%s", kind, actual, expected)
		}
	}
}
