package clibootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func TestNativeRemoteAgentRestoreRejectsNilForeignHeadlessAndClosedAuthority(t *testing.T) {
	ctx := context.Background()
	for _, main := range []*MainPrint{nil, {}} {
		if err := main.StartTerminalRemoteTasks(ctx); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("nil/unbound remote task authority", err)
		}
	}
	m := taskMainFixture(t)
	if err := m.StartTerminalRemoteTasks(ctx); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("headless task restoration acquired REPL authority", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := m.beginNativeRemoteAgentMetadata(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("foreign metadata consumer", err)
		}
		if _, err := m.nativeRemoteAgentAPI(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("foreign API consumer", err)
		}
		if err := m.restoreNativeRemoteAgentTasks(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("foreign restore consumer", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		m.BeginNativeRemoteAgentTasksClose()
		if _, err := m.nativeRemoteAgentLife.launch(m, func(context.Context, *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
			t.Fatal("closed lifetime ran a producer")
			return nil, nil
		}); !errors.Is(err, cooperative.ErrClosed) {
			t.Fatal("closed restoration restarted native work", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
