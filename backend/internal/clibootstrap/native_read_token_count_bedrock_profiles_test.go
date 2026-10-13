package clibootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualNativeBedrockReadCountProfileMemoizesFirstBackingModelAndUnavailable(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "backing-model"}[available], func(t *testing.T) {
			var profiles, counts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("content-type", "application/json")
				if strings.HasPrefix(r.URL.Path, "/inference-profiles/") {
					profiles.Add(1)
					if available {
						io.WriteString(w, `{"models":[{"modelArn":"arn:aws:bedrock:us-east-1:123456789012:foundation-model/anthropic.claude-sonnet-4-5-20250929-v1:0"},{"modelArn":"must-not-select-second"}]}`)
					} else {
						io.WriteString(w, `{"models":[]}`)
					}
					return
				}
				if !strings.HasSuffix(r.URL.Path, "anthropic.claude-sonnet-4-5-20250929-v1:0/count-tokens") {
					t.Error("profile ARN was sent as a count model", r.URL.Path)
				}
				counts.Add(1)
				io.WriteString(w, `{"inputTokens":42}`)
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				n.Environment["CLAUDE_CODE_USE_BEDROCK"] = "1"
				n.Environment["CLAUDE_CODE_SKIP_BEDROCK_AUTH"] = "1"
				n.Environment["ANTHROPIC_BEDROCK_BASE_URL"] = peer.URL
			}, func(o *MainPrintOptions) {
				o.Arguments.Model = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
				o.Arguments.Tools = []string{"Read"}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for i := 0; i < 2; i++ {
				if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					value, err := m.nativeReadTokenCount(ctx, scope, "actual private profile count")
					if available {
						if value == nil || *value != 42 {
							t.Error("profile count not consumed")
						}
					} else if value != nil {
						t.Error("unavailable backing profile guessed a count")
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			expected := int32(0)
			if available {
				expected = 2
			}
			if profiles.Load() != 1 || counts.Load() != expected {
				t.Fatal("profile Promise cache/missing-model fallback changed", profiles.Load(), counts.Load())
			}
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestActualBedrockReadCountRuntimeCloseCancelsBeforeHeaders(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_USE_BEDROCK"] = "1"
		n.Environment["CLAUDE_CODE_SKIP_BEDROCK_AUTH"] = "1"
		n.Environment["ANTHROPIC_BEDROCK_BASE_URL"] = peer.URL
	}, func(o *MainPrintOptions) {
		o.Arguments.Model = "anthropic.claude-sonnet-4-5-20250929-v1:0"
		o.Arguments.Tools = []string{"Read"}
	})
	done := make(chan error, 1)
	go func() {
		done <- m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			_, err := m.nativeReadTokenCount(context.Background(), scope, "actual private count")
			return err
		})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("actual Bedrock count never started")
	}
	m.BeginNativePDFRuntimeClose()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal("closed runtime count did not settle canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Bedrock count retained owner after runtime close")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Bedrock runtime left physical HTTP alive")
	}
	if err := m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
}
