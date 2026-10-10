package application

import (
	"bytes"
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestConfiguredAgentEffortAndBuiltinClaudeMdOmissionReachActualChildPOST(t *testing.T) {
	for _, tc := range []struct {
		agent, effort, wantEffort string
		omit                      bool
	}{{"effort-worker", "low", "low", false}, {"effort-worker", "", "high", false}, {"Explore", "", "high", true}} {
		t.Run(tc.agent+tc.wantEffort, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				request := obj(body)
				switch calls.Add(1) {
				case 1:
					if !bytes.Contains(body, []byte("REAL PARENT CLAUDE MD CONTEXT")) {
						t.Error("actual parent CLAUDE.md missing")
					}
					response(w, "context-parent", "Agent", map[string]any{"prompt": "Return a short child report.", "description": "Context worker", "subagent_type": tc.agent}, "")
				case 2:
					if request.String("model") != "claude-opus-5-5" {
						t.Errorf("actual definition model %s", request.String("model"))
					}
					output := obj(request.Get("output_config"))
					if output.String("effort") != tc.wantEffort {
						t.Errorf("actual child effort %s want%s output body%.700s", output.Get("effort"), tc.wantEffort, body)
					}
					has := bytes.Contains(body, []byte("REAL PARENT CLAUDE MD CONTEXT"))
					if has == tc.omit {
						t.Errorf("child omitClaudeMd=%v bodyHasParentContext=%v", tc.omit, has)
					}
					response(w, "context-child", "", nil, "CONTEXT CHILD DONE")
				case 3:
					if !bytes.Contains(body, []byte("REAL PARENT CLAUDE MD CONTEXT")) {
						t.Error("child mutation removed parent context")
					}
					response(w, "context-parent-done", "", nil, "CONTEXT PARENT DONE")
				default:
					t.Error("extra configured context request")
					http.Error(w, "unexpected", 400)
				}
			}))
			defer provider.Close()
			options := nativeSessionOptions(t, provider.URL)
			options.Arguments.Model = "claude-opus-5-5"
			options.Arguments.Tools = []string{"Read,Agent"}
			options.Arguments.EffortLevel = "high"
			options.Native.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
			options.Native.Environment["CLAUDE_CODE_ALWAYS_ENABLE_EFFORT"] = "1"
			options.Native.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "claude-opus-5-5"
			if err := os.WriteFile(filepath.Join(options.WorkDir, "CLAUDE.md"), []byte("REAL PARENT CLAUDE MD CONTEXT\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.agent != "Explore" {
				base := filepath.Join(options.Environment["CLAUDE_CONFIG_DIR"], "agents")
				_ = os.MkdirAll(base, 0700)
				effort := ""
				if tc.effort != "" {
					effort = "effort: " + tc.effort + "\n"
				}
				data := "---\nname: effort-worker\ndescription: Test actual effort worker\ntools: Read\nmodel: claude-opus-5-5\n" + effort + "---\nReturn the child report."
				if err := os.WriteFile(filepath.Join(base, "worker.md"), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			session, err := NewSession(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close(context.Background())
			if err = session.Submit(context.Background(), tui.Input{Text: "Run the configured context worker."}, nil); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 3 {
				t.Fatalf("actual child request count%d", calls.Load())
			}
		})
	}
}
