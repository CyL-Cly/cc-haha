package modelyield

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestOwnedSDKGitModelPhaseSharesFalseAdmissionWithOpener(t *testing.T) {
	for _, disabled := range []string{"", "1", "0"} {
		for _, sample := range []string{"nil", "false", "true"} {
			t.Run(disabled+"-"+sample, func(t *testing.T) {
				var sent atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sent.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, responseWire)
				}))
				defer server.Close()
				env := map[string]string{"ANTHROPIC_API_KEY": "fake", "ANTHROPIC_BASE_URL": server.URL}
				if disabled != "" {
					env["CLAUDE_CODE_DISABLE_FAST_MODE"] = disabled
				}
				input, e := contextFromFixture(fixtureInput{Env: env, Model: "deepseek-flash", MaxOutputTokens: 32000, DeviceID: "device", SessionID: "session", Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}})
				if e != nil {
					t.Fatal(e)
				}
				if sample != "nil" {
					value := sample == "true"
					input.ResolvedFastMode = &value
				}
				owner := cooperative.New()
				defer owner.Close()
				committed := 0
				e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					_, e := Run(context.Background(), input, Dependencies{Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "original" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil }}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
						if value.Update != nil && value.Update.Envelope != nil {
							committed++
						}
						return requestretry.Resume, nil
					}})
					return e
				})
				want := disabled != "0" && sample != "true" && (disabled == "1" || sample == "false")
				if (e == nil) != want || want && (sent.Load() != 1 || committed != 1) || !want && sent.Load() != 0 {
					t.Fatalf("phase/opener e=%v requests=%d committed=%d want=%v", e, sent.Load(), committed, want)
				}
			})
		}
	}
}
