package sdkhost

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestActualHostAbortHandlerConsumesMainComputerUseCleanupExactlyOnce(t *testing.T) {
	home := t.TempDir()
	state, err := clisettings.NewState(func() float64 { return 1700000000000 })
	if err != nil {
		t.Fatal(err)
	}
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: filepath.Join(home, ".claude"), OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1700000000000 }, PID: 12345})
	if err != nil {
		t.Fatal(err)
	}
	hooks, err := queryhooks.New(queryhooks.Options{Profile: queryhooks.EmptyExternalProfile, Settings: settings, SettingsState: state, OriginalEnv: map[string]string{"CLAUDE_CODE_SIMPLE": "1"}, MainSessionID: "private-main", PolicyAbsent: true, FlagAbsent: true, PluginBaseAbsent: true, UUID: func() string { return "private-hook" }})
	if err != nil {
		t.Fatal(err)
	}
	host := &Host{hooks: hooks}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	call := queryhooks.Context{ComputerUseInactive: false}
	calls, contexts := 0, 0
	call.CleanupComputerUse = func(cleanup context.Context) error {
		calls++
		if cleanup.Err() != nil {
			t.Error("Host propagated model cancellation to cleanup")
		}
		return errors.New("best-effort native failure")
	}
	var stage queryloop.Round
	cleanup := host.abortCleanupHandler(func(round queryloop.Round) queryhooks.Context { contexts++; return call })
	for range 2 {
		if err = cleanup(ctx, stage); err != nil {
			t.Fatal(err)
		}
		if calls != contexts {
			t.Fatalf("Host duplicated cleanup: calls=%d contexts=%d", calls, contexts)
		}
	}
	call.AgentID = "child-agent"
	if err = cleanup(ctx, stage); err != nil || calls != 2 {
		t.Fatalf("child released main resource: calls=%d err=%v", calls, err)
	}
	call.AgentID = ""
	call.CleanupComputerUse = nil
	var capability *queryhooks.CapabilityError
	if err = cleanup(ctx, stage); !errors.As(err, &capability) || calls != 2 {
		t.Fatalf("missing native binding silently admitted: %v", err)
	}
	call.ComputerUseInactive = true
	if err = cleanup(ctx, stage); err != nil || calls != 2 {
		t.Fatalf("inactive turn allocated cleanup: %v", err)
	}
}
