package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeActualPlanAutoTrustedOptInEnterAndExit(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	if e := os.WriteFile(filepath.Join(f.config, "settings.json"), []byte(`{"skipAutoPermissionPrompt":true,"autoQuestion":{"enabled":true,"timeoutMinutes":1}}`), 0600); e != nil {
		t.Fatal(e)
	}
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual original trusted auto setting", Tools: []string{"EnterPlanMode,ExitPlanMode,Read"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 4, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, tools, e := m.nativeTools.Catalog(scope)
		if e != nil {
			return e
		}
		for _, name := range []string{"EnterPlanMode", "ExitPlanMode"} {
			var tool queryloop.RegisteredTool
			for _, candidate := range tools {
				if candidate.Name == name {
					tool = candidate
					break
				}
			}
			in := protocol.NewJSONObject()
			if name == "ExitPlanMode" {
				in.Set("plan", quoteMain("Original private plan"))
			}
			pending, e := tool.StartCallJSON(context.Background(), in, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
			if e != nil {
				return e
			}
			if _, e = pending.Await(scope); e != nil {
				return e
			}
			actual, e := m.Controller.PlanModeContext(scope)
			if e != nil {
				return e
			}
			if name == "EnterPlanMode" && (actual.String("mode") != "plan" || actual.String("prePlanMode") != "default" || string(actual.Get("strippedDangerousRules")) != "{}") {
				return fmt.Errorf("Source active plan state missing: %s", rawMain(actual))
			}
			if name == "ExitPlanMode" && (actual.String("mode") != "default" || actual.Get("prePlanMode") != nil || actual.Get("strippedDangerousRules") != nil) {
				return fmt.Errorf("Source restored context missing: %s", rawMain(actual))
			}
			active, e := m.Controller.NativeAutoModeActive(scope)
			if e != nil {
				return e
			}
			if name == "EnterPlanMode" && !active {
				return fmt.Errorf("trusted plan classifier inactive")
			}
			if name == "ExitPlanMode" {
				if active {
					return fmt.Errorf("classifier retained after original exit")
				}
				once, e := m.state.TakeAutoModeExitAttachment(scope, active)
				if e != nil {
					return e
				}
				twice, e := m.state.TakeAutoModeExitAttachment(scope, active)
				if e != nil {
					return e
				}
				if !once || twice {
					return fmt.Errorf("Source once exit latch: %v/%v", once, twice)
				}
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
