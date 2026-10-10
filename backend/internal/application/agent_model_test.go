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

func TestAgentModelSelectionReachesSharedChildHarness(t *testing.T) {
	for _, tc := range []struct{ name, parent, tool, configured, sonnet, want string }{
		{"inherit-parent", "PinnedParent", "", "", "", "PinnedParent"},
		{"same-tier-retains-version", "claude-opus-4-7", "opus", "", "", "claude-opus-4-7"},
		{"configured-before-tool", "PinnedParent", "sonnet", " CustomChild ", "AliasChild", "CustomChild"},
		{"tier-default-env", "PinnedParent", "sonnet", "", "AliasChild", "AliasChild"},
		{"case-insensitive-configured-inherit", "PinnedParent", "", " INHERIT ", "", "PinnedParent"},
		{"inherit-plan-host-model", "haiku", "", "", "AliasChild", "AliasChild"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				switch calls.Add(1) {
				case 1:
					input := map[string]any{"prompt": "Report child completion.", "description": "real model selection", "subagent_type": "general-purpose"}
					if tc.tool != "" {
						input["model"] = tc.tool
					}
					response(w, "model-parent-agent", "Agent", input, "")
				case 2:
					if model := obj(body).String("model"); model != tc.want {
						t.Errorf("actual child model=%q want=%q", model, tc.want)
					}
					response(w, "model-child-complete", "", nil, "CHILD MODEL OK")
				case 3:
					response(w, "model-parent-complete", "", nil, "PARENT MODEL OK")
				default:
					t.Error("extra model consumer")
					http.Error(w, "unexpected", 400)
				}
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.Model = tc.parent
			o.Arguments.Tools = []string{"Read,Agent"}
			o.Native.Environment["CLAUDE_CODE_SUBAGENT_MODEL"] = tc.configured
			o.Native.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = tc.sonnet
			if tc.name == "inherit-plan-host-model" {
				o.Native.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "NativeHaiku"
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if tc.name == "inherit-plan-host-model" {
				if err = s.process.SetSessionPermissionMode(context.Background(), "plan"); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.Submit(context.Background(), tui.Input{Text: "Run the real child Agent."}, nil); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 3 {
				t.Fatalf("actual child did not finish: calls=%d", calls.Load())
			}
		})
	}
}
