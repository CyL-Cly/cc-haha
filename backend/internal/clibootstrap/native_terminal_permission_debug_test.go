package clibootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeTerminalPermissionDebugSnapshotUsesLiveContextAndActualCLITruthyFlags(t *testing.T) {
	for _, row := range []struct {
		name string
		args []string
		hint bool
	}{
		{"plain", nil, false}, {"debug", []string{"--debug"}, true}, {"debug-filter-false-string", []string{"--debug=false"}, true}, {"empty-debug-filter", []string{"--debug="}, true}, {"stderr", []string{"--debug-to-stderr"}, false}, {"d2e", []string{"-d2e"}, true}, {"file-only", []string{"--debug-file", "$PRIVATE"}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := worktreeMainFixture(t)
			f.n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
			f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"enabledPlugins":{"sample@mkt":false},"permissions":{"ask":["Bash","PowerShell"],"allow":["Read"],"deny":["Bash(rm:*)"]}}`)
			args := append([]string{}, row.args...)
			for i := range args {
				if args[i] == "$PRIVATE" {
					args[i] = filepath.Join(f.home, "debug-private.log")
				}
			}
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Native: f.n, Interactive: true, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{DebugArguments: args, ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 2, Tools: []string{"Bash"}, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if e = m.StartSession(ctx); e != nil {
				t.Fatal(e)
			}
			if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				actual, hint, sandbox, e := m.TerminalPermissionDebugSnapshot(scope)
				if e != nil {
					return e
				}
				current, e := m.Host.NativePermissionContext(scope)
				if e != nil {
					return e
				}
				if !reflect.DeepEqual(actual, current) || hint != row.hint || sandbox {
					t.Error("actual permission/debug/sandbox snapshot", actual, hint, sandbox, current, row.hint)
				}
				if !strings.Contains(string(rawMain(actual)), "Bash") || !strings.Contains(string(rawMain(actual)), "rm") {
					t.Error("actual full context rules lost", actual)
				}
				actual.Set("mode", quoteMain("mutated-copy"))
				after, e := m.Host.NativePermissionContext(scope)
				if e != nil {
					return e
				}
				if after.String("mode") == "mutated-copy" {
					t.Error("snapshot mutated live context")
				}
				if e = m.Host.ApplyNativePermissionUpdates(scope, []byte(`[{"type":"setMode","mode":"acceptEdits","destination":"session"}]`)); e != nil {
					return e
				}
				changed, _, _, e := m.TerminalPermissionDebugSnapshot(scope)
				if e != nil {
					return e
				}
				if changed.String("mode") != "acceptEdits" {
					t.Error("snapshot froze startup context", changed)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestNativeTerminalPermissionDebugSnapshotRejectsNilForeignRetainedAndClosedScopes(t *testing.T) {
	f := worktreeMainFixture(t)
	f.n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 2, Tools: []string{"Bash"}, Sources: DefaultState()}})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	for _, scope := range []*cooperative.Scope{nil, {}} {
		if _, _, _, e = m.TerminalPermissionDebugSnapshot(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("invalid debug scope admitted", e)
		}
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if e = foreign.Run(ctx, func(scope *cooperative.Scope) error { _, _, _, e := m.TerminalPermissionDebugSnapshot(scope); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("foreign debug scope admitted", e)
	}
	var retained *cooperative.Scope
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		retained = scope
		_, _, _, e := m.TerminalPermissionDebugSnapshot(scope)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = m.TerminalPermissionDebugSnapshot(retained); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("retained scope admitted", e)
	}
	m.Close()
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error { _, _, _, e := m.TerminalPermissionDebugSnapshot(scope); return e }); !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal("closed Owner admitted debug", e)
	}
	if _, _, _, e = m.TerminalPermissionDebugSnapshot(retained); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("closed retained debug scope admitted", e)
	}
	var absent *MainPrint
	if _, _, _, e = absent.TerminalPermissionDebugSnapshot(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("nil process admitted debug", e)
	}
}
