package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestPermissionModeChangeResolvesModelInActualHarness(t *testing.T) {
	for _, tc := range []struct {
		name, alias string
		want        []string
	}{
		{"haiku", "haiku", []string{"NativeHaiku", "NativeSonnet", "NativeHaiku"}},
		{"opusplan", "opusplan", []string{"NativeSonnet", "NativeOpus", "NativeSonnet"}},
		{"case-is-not-runtime-alias", "Haiku", []string{"NativeHaiku", "NativeHaiku", "NativeHaiku"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				at := int(calls.Add(1)) - 1
				if at >= len(tc.want) {
					t.Error("extra request")
					http.Error(w, "unexpected", 400)
					return
				}
				if got := obj(body).String("model"); got != tc.want[at] {
					t.Errorf("request %d model=%q want=%q", at, got, tc.want[at])
				}
				response(w, "mode-response", "", nil, "ACTUAL MODE OK")
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.Model = tc.alias
			o.Arguments.Tools = []string{"Read"}
			o.Arguments.PermissionMode = "default"
			for tier, model := range map[string]string{"HAIKU": "NativeHaiku", "SONNET": "NativeSonnet", "OPUS": "NativeOpus"} {
				o.Native.Environment["ANTHROPIC_DEFAULT_"+tier+"_MODEL"] = model
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			for _, mode := range []string{"default", "plan", "default"} {
				if err = s.process.SetSessionPermissionMode(context.Background(), mode); err != nil {
					t.Fatal(err)
				}
				if err = s.Submit(context.Background(), tui.Input{Text: "Resolve the current permission mode."}, nil); err != nil {
					t.Fatal(err)
				}
			}
			if calls.Load() != 3 {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}
}
