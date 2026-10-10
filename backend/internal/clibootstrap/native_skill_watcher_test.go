package clibootstrap

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillwatcher"
)

func TestMainPrintPhysicalConfigChangesPublishSameOwnerCommandsAndDrainWatcher(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	path := filepath.Join(f.config, "cc-haha", "computer-use-config.json")
	f.write(t, path, `{"enabled":false,"future":{"keep":true}}`)
	options.SkillWatchTiming = &skillwatcher.Timing{Interval: 10 * time.Millisecond, Stability: 50 * time.Millisecond, StabilityPoll: 10 * time.Millisecond, Debounce: 30 * time.Millisecond, Atomic: 10 * time.Millisecond}
	var now atomic.Int64
	now.Store(5000)
	f.n.NowMillis = func() float64 { return float64(now.Load()) }
	updates := make(chan []sdkhost.ControlCommand, 8)
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, OnCommandsChanged: func(commands []sdkhost.ControlCommand) error { updates <- commands; return nil }, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "no model needed to watch configuration", Tools: []string{"Read"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 8, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e := m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	has := func(commands []sdkhost.ControlCommand, name string) bool {
		for _, command := range commands {
			if command.Name == name {
				return true
			}
		}
		return false
	}
	read := func() []sdkhost.ControlCommand {
		var commands []sdkhost.ControlCommand
		if e := m.Owner.Run(context.Background(), func(*cooperative.Scope) error { commands = m.Registry.ControlCommands(); return nil }); e != nil {
			t.Fatal(e)
		}
		return commands
	}
	if has(read(), "computer-use") {
		t.Fatal("disabled skill present initially")
	}
	changed := func(enabled bool) {
		t.Helper()
		now.Add(4000)
		text := `{"enabled":false,"future":{"keep":true}}`
		if enabled {
			text = `{"enabled":true,"future":{"keep":true}}`
		}
		f.write(t, path, text)
		select {
		case commands := <-updates:
			if has(commands, "computer-use") != enabled {
				t.Fatal("notification contains stale config", commands)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("physical configuration change did not notify")
		}
		if has(read(), "computer-use") != enabled {
			t.Fatal("menu and shared Harness Registry diverged")
		}
	}
	changed(true)
	changed(false)
	if e := m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.write(t, path, `{"enabled":true}`)
	time.Sleep(150 * time.Millisecond)
	select {
	case <-updates:
		t.Fatal("closed Harness emitted commands")
	default:
	}
}
