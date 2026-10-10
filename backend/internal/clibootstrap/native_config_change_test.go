package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillwatcher"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPhysicalConfigChangeHookBlocksThenAllowsSameRegistryReload(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	config := filepath.Join(f.config, "cc-haha", "computer-use-config.json")
	allow := filepath.Join(f.cwd, "allow-reload")
	journal := filepath.Join(f.cwd, "config-hook.jsonl")
	command := `cat >> "` + journal + `"; printf '\n' >> "` + journal + `"; if [ -f "` + allow + `" ]; then printf '{}'; else printf '{"decision":"block"}'; fi`
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "hooks": map[string]any{"ConfigChange": []any{map[string]any{"matcher": "skills", "hooks": []any{map[string]any{"type": "command", "command": command}}}}}})))
	f.write(t, config, `{"enabled":false}`)
	options.SkillWatchTiming = &skillwatcher.Timing{Interval: 10 * time.Millisecond, Stability: 50 * time.Millisecond, StabilityPoll: 10 * time.Millisecond, Debounce: 30 * time.Millisecond, Atomic: 10 * time.Millisecond}
	var clock atomic.Int64
	clock.Store(5000)
	f.n.NowMillis = func() float64 { return float64(clock.Load()) }
	updates := make(chan []sdkhost.ControlCommand, 10)
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{ComputerUse: options, OnCommandsChanged: func(commands []sdkhost.ControlCommand) error { updates <- commands; return nil }, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 8, SystemPrompt: "no model for actual configuration watcher", Tools: []string{"Read"}, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownSession(context.Background())
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Add(4000)
	f.write(t, config, `{"enabled":true}`)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if data, err := os.ReadFile(journal); err == nil && strings.Contains(string(data), `"source":"skills"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual ConfigChange command did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-updates:
		t.Fatal("blocking original hook still reloaded Registry")
	case <-time.After(100 * time.Millisecond):
	}
	f.write(t, allow, "approved")
	clock.Add(4000)
	f.write(t, config, `{"enabled":true,"future":{"keep":true}}`)
	select {
	case commands := <-updates:
		found := false
		for _, command := range commands {
			found = found || command.Name == "computer-use"
		}
		if !found {
			t.Fatal("actual allowed reload kept stale menu")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("actual allowed change did not notify")
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"hook_event_name":"ConfigChange"`) != 2 {
		t.Fatal("batched Hook invocations", string(data))
	}
}
