package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestConfiguredAgentUsesActualDefinitionModelPromptToolsAndReload(t *testing.T) {
	for _, location := range []string{"user", "project"} {
		t.Run(location, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				ordinal := calls.Add(1)
				reloaded := ordinal >= 4
				prompt := "CUSTOM AGENT INSTRUCTIONS"
				model := "ConfiguredChildModel"
				if reloaded {
					prompt, model = "RELOADED AGENT INSTRUCTIONS", "ReloadedChildModel"
				}
				switch ordinal {
				case 1, 4:
					if !bytes.Contains(body, []byte("review-worker")) || !bytes.Contains(body, []byte("Custom saved reviewer")) {
						t.Error("active configured agent absent from parent Agent schema")
					}
					response(w, "configured-parent", "Agent", map[string]any{"prompt": "Review using the configured definition.", "description": "Run custom reviewer", "subagent_type": "review-worker"}, "")
				case 2, 5:
					request := obj(body)
					if request.String("model") != model || !bytes.Contains(body, []byte(prompt)) || bytes.Contains(body, []byte("Searching for code, configurations, and patterns across large codebases")) {
						t.Errorf("custom actual model/prompt lost: model%q instruction%s", request.String("model"), prompt)
						t.Logf("original native history %.1800s", request.Get("messages"))
					}
					var schemas []protocol.JSONObject
					_ = json.Unmarshal(request.Get("tools"), &schemas)
					if len(schemas) != 1 || schemas[0].String("name") != "Read" {
						t.Errorf("configured tool allow/deny policy lost: schema count%d", len(schemas))
					}
					if bytes.Contains(body, []byte(`"name":"AskUserQuestion"`)) || bytes.Contains(body, []byte(`"name":"Agent"`)) {
						t.Error("Source forbidden worker tool entered child")
					}
					response(w, "configured-child", "", nil, "CUSTOM CHILD FINISHED")
				case 3, 6:
					if !bytes.Contains(body, []byte("CUSTOM CHILD FINISHED")) || !bytes.Contains(body, []byte("review-worker")) {
						t.Error("real configured child result did not reenter parent")
					}
					response(w, "configured-parent-final", "", nil, "CONFIGURED PARENT FINISHED")
				default:
					t.Errorf("unexpected configured Agent request %d", ordinal)
				}
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.Model = "ParentModel"
			o.Arguments.Tools = []string{"Agent,Read,Write"}
			o.Native.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
			base := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "agents")
			if location == "project" {
				base = filepath.Join(o.WorkDir, ".claude", "agents")
			}
			_ = os.MkdirAll(base, 0700)
			path := filepath.Join(base, "review.md")
			write := func(model, prompt string) {
				t.Helper()
				content := "---\nname: review-worker\ndescription: Custom saved reviewer\ntools: Read, Write, AskUserQuestion, Agent\ndisallowedTools: Write\nmodel: " + model + "\npermissionMode: plan\nmaxTurns: 4\n---\n" + prompt
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("ConfiguredChildModel", "CUSTOM AGENT INSTRUCTIONS")
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if err = s.Submit(context.Background(), tui.Input{Text: "Run the saved custom agent."}, nil); err != nil {
				t.Fatal(err)
			}
			write("ReloadedChildModel", "RELOADED AGENT INSTRUCTIONS")
			result, err := s.process.ReloadComponents(context.Background())
			if err != nil || !strings.Contains(string(result.Get("agents")), "review-worker") {
				t.Fatalf("same session agent reload %s %v", result.Get("agents"), err)
			}
			if err = s.Submit(context.Background(), tui.Input{Text: "Run the changed saved custom agent."}, nil); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 6 {
				t.Fatalf("configured Agent never executed real child twice: %d", calls.Load())
			}
		})
	}
}
