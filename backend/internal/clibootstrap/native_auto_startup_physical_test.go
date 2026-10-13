package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualNativeAutoStartupCLIAndSavedModeClassifyBeforePhysicalShell(t *testing.T) {
	for _, entry := range []string{"cli", "saved"} {
		t.Run(entry, func(t *testing.T) {
			f, _, cu := computerUseMainFixture(t)
			delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
			settings := `{"skipAutoPermissionPrompt":true,"autoMode":{"classifyAllShell":true},"permissions":{"allow":["Bash(*)","Agent"]}}`
			if entry == "saved" {
				settings = `{"skipAutoPermissionPrompt":true,"autoMode":{"classifyAllShell":true},"permissions":{"defaultMode":"auto","allow":["Bash(*)","Agent"]}}`
			}
			f.write(t, filepath.Join(f.config, "settings.json"), settings)
			proof := filepath.Join(f.cwd, "auto-startup-proof.txt")
			var mu sync.Mutex
			modelRequests, classifierRequests := 0, 0
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var request protocol.JSONObject
				_ = request.UnmarshalJSON(body)
				if string(request.Get("stream")) != "true" {
					mu.Lock()
					classifierRequests++
					mu.Unlock()
					if _, err := os.Stat(proof); !os.IsNotExist(err) {
						t.Error("physical command ran before actual classifier response", err)
					}
					if !bytes.Contains(body, []byte(`"name":"classify_result"`)) {
						t.Error("classifier request lost original result schema")
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"native-auto-startup-classifier","content":[{"type":"tool_use","name":"classify_result","input":{"thinking":"actual owned fixture","shouldBlock":false,"reason":"only the owned test marker"}}],"usage":{"input_tokens":2,"output_tokens":1}}`)
					return
				}
				mu.Lock()
				modelRequests++
				round := modelRequests
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if round == 1 {
					call := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("tool_use")}, protocol.JSONField{Name: "id", Value: quoteMain("native-auto-startup-action")}, protocol.JSONField{Name: "name", Value: quoteMain("Bash")}, protocol.JSONField{Name: "input", Value: rawMain(map[string]string{"command": "printf AUTO_STARTUP_OK > '" + proof + "'", "description": "Create only the owned fixture marker"})})
					fmt.Fprint(w, webWire("fixture-model", []protocol.JSONObject{call}))
				} else if round == 2 {
					fmt.Fprint(w, webWire("fixture-model", []protocol.JSONObject{webObject(`{"type":"text","text":"actual auto startup shell complete"}`)}))
				} else {
					t.Error("unexpected model request", round)
				}
			}))
			defer model.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
			disabled := false
			mode := "auto"
			if entry == "saved" {
				mode = ""
			}
			m, err := NewMainPrint(MainPrintOptions{ComputerUse: cu, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual Auto startup fixture", Tools: []string{"Bash"}, SessionID: "33333333-3333-4333-8333-333333333333", MaxTurns: 5, PermissionMode: mode, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			if err = m.StartSession(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				permission, err := m.Host.NativePermissionContext(scope)
				if err != nil {
					return err
				}
				active, err := m.Controller.NativeAutoModeActive(scope)
				if err != nil {
					return err
				}
				if permission.String("mode") != "auto" || !active || permission.Get("strippedDangerousRules") == nil {
					return fmt.Errorf("startup auto/rules not initialized: %s active=%v", rawMain(permission), active)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err = m.RunTurn(context.Background(), webObject(`{"type":"user","message":{"role":"user","content":"Only create the declared owned marker through the tool"}}`)); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(proof); err != nil || string(data) != "AUTO_STARTUP_OK" {
				t.Fatal("actual shell result", err, string(data))
			}
			mu.Lock()
			if classifierRequests != 1 || modelRequests != 2 {
				t.Errorf("actual main/classifier requests=%d/%d", modelRequests, classifierRequests)
			}
			mu.Unlock()
			if next, err := m.CycleTerminalPermissionMode(context.Background()); err != nil || next != "default" {
				t.Fatal("active auto could not return control", next, err)
			}
			if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				permission, err := m.Host.NativePermissionContext(scope)
				if err != nil {
					return err
				}
				active, err := m.Controller.NativeAutoModeActive(scope)
				if err != nil {
					return err
				}
				if active || permission.Get("strippedDangerousRules") != nil || !strings.Contains(string(permission.Get("alwaysAllowRules")), `"Bash"`) {
					return fmt.Errorf("auto exit lost original rules: %s", rawMain(permission))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
