package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func TestInteractivePowerShellEditedRulePersistsAndNextTurnUsesRealExecution(t *testing.T) {
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("physical native PowerShell required")
	}
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 || n == 3 {
			response(w, fmt.Sprintf("ps-write-%d", n), "PowerShell", map[string]any{"command": fmt.Sprintf("touch private-powershell-%d.txt", n)}, "")
		} else {
			response(w, fmt.Sprintf("ps-done-%d", n), "", nil, "PRIVATE_COMPLETE")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Interactive = true
	o.Arguments.Tools = []string{"PowerShell"}
	o.Arguments.PermissionMode = "default"
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CLAUDE_CODE_POWERSHELL_PATH"] = executable
		env["CLAUDE_CODE_USE_POWERSHELL_TOOL"] = "1"
		env["TMPDIR"] = o.Native.Home
		env["CLAUDE_CODE_TMPDIR"] = o.Native.Home
	}
	// Controlled Windows shell-discovery input with the real Darwin file and
	// process boundary. This is not evidence from a native Windows computer.
	o.NativeShells = windowshell.NewNative("windows", o.WorkDir, o.Native.Environment)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	responses := make(chan error, 2)
	asks := 0
	for turn := 0; turn < 2; turn++ {
		err = s.Submit(ctx, tui.Input{Text: "Perform the private PowerShell write."}, func(event tui.Event) error {
			if event.Kind != tui.PermissionAsked {
				return nil
			}
			asks++
			request := *event.Permission
			if len(request.Choices) != 3 || !request.Choices[1].Editable || request.Choices[1].InitialValue != "touch:*" {
				return fmt.Errorf("actual terminal PS approval options: %+v", request.Choices)
			}
			// The terminal editor's key handling/action is checked in package tui;
			// this physical Session consumer receives the explicit edited decision.
			prefix := " touch:* "
			decision := tui.PermissionDecision{Allowed: true, EditedPrefix: &prefix}
			go func() { responses <- s.RespondPermission(ctx, request.ID, decision) }()
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if turn == 0 {
			if asks != 1 {
				t.Fatalf("expected one actual PowerShell permission before turn completion, got %d", asks)
			}
			select {
			case err := <-responses:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	if asks != 1 || requests.Load() != 4 {
		t.Fatalf("persisted actual rule did not bypass second prompt: asks=%d requests=%d", asks, requests.Load())
	}
	file, err := os.ReadFile(filepath.Join(o.WorkDir, "private-powershell-3.txt"))
	if err != nil || len(file) != 0 {
		t.Fatal("actual second PS execution", err, string(file))
	}
	settings, err := os.ReadFile(filepath.Join(o.WorkDir, ".claude", "settings.local.json"))
	if err != nil || !strings.Contains(string(settings), "PowerShell(touch:*)") {
		t.Fatal("actual original localSettings persistence", err, string(settings))
	}
}
