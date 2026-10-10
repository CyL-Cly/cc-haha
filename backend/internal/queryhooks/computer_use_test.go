package queryhooks

import (
	"context"
	"errors"
	"testing"
)

func TestComputerUseStopUsesBoundCleanupBeforeHooksWithoutHoldingResolverLock(t *testing.T) {
	service, _, count := newTestService(t, nil)
	if e := service.SetSession("hook-main", "Stop", []Matcher{{Hooks: []string{}}}); e != nil {
		t.Fatal(e)
	}
	call := emptyContext()
	call.ComputerUseInactive = false
	calls := 0
	call.CleanupComputerUse = func(context.Context) error {
		calls++
		if service.CacheSafeParams() == nil {
			t.Fatal("cleanup ran before actual cache-safe snapshot")
		}
		if count.Load() != 0 {
			t.Fatal("Stop hooks allocated before CU cleanup")
		}
		return errors.New("native best-effort cleanup failed")
	}
	if _, e := service.StopHooks(context.Background(), call, rejectYield); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || count.Load() != 1 {
		t.Fatalf("cleanup/hooks=%d/%d", calls, count.Load())
	}
	call.AgentID = "actual-child"
	if _, e := service.StopHooks(context.Background(), call, rejectYield); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("child cleanup released parent native lease")
	}
	call.AgentID = ""
	call.CleanupComputerUse = nil
	_, e := service.StopHooks(context.Background(), call, rejectYield)
	requireCapability(t, e)
	call.CleanupComputerUse = func(context.Context) error { t.Fatal("unported goal/team policy executed cleanup"); return nil }
	call.GoalInactive = false
	_, e = service.StopHooks(context.Background(), call, rejectYield)
	requireCapability(t, e)
}

func TestComputerUseAbortUsesBoundCleanupWithoutCanceledModelSignal(t *testing.T) {
	service, _, _ := newTestService(t, nil)
	call := emptyContext()
	call.ComputerUseInactive = false
	type markerKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), markerKey{}, "original owner"))
	cancel()
	calls := 0
	call.CleanupComputerUse = func(cleanupContext context.Context) error {
		calls++
		if cleanupContext.Err() != nil || cleanupContext.Done() != nil {
			t.Fatal("aborted model signal canceled native cleanup")
		}
		if cleanupContext.Value(markerKey{}) != "original owner" {
			t.Fatal("cleanup lost original owner context")
		}
		return errors.New("native best-effort overlay failure")
	}
	if err := service.AbortCleanup(ctx, call); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("actual cleanup calls=%d", calls)
	}
	call.AgentID = "child-with-parent-computer-use"
	if err := service.AbortCleanup(ctx, call); err != nil || calls != 1 {
		t.Fatalf("child released parent lease: calls=%d error=%v", calls, err)
	}
	call.AgentID = ""
	call.CleanupComputerUse = nil
	requireCapability(t, service.AbortCleanup(ctx, call))
}
