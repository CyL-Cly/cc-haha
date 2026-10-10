package toolsearch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoizedAutoTokenPromiseHasOneProducerAndExplicitOwnerWait(t *testing.T) {
	service := New()
	entered, release := make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	tools := []Tool{{Name: quoted("ToolSearch")}, {Name: quoted("mcp__actual__read"), IsMCP: true}}
	inputs := GateInputs{Environment: map[string]string{"ENABLE_TOOL_SEARCH": "auto"}, ContextWindow: func(context.Context, string) (float64, error) { return 200000, nil }, CountDefinitionTokens: func(context.Context, []Tool, string) (*float64, error) {
		if count.Add(1) != 1 {
			t.Error("duplicated original count producer")
		}
		close(entered)
		<-release
		n := 20500.0
		return &n, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() {
		plan, e := service.Plan(ctx, "fixture-model", tools, nil, false, inputs)
		if e == nil && !plan.Enabled {
			t.Error("wrong first decision")
		}
		first <- e
	}()
	<-entered
	if _, e := service.Plan(ctx, "fixture-model", tools, nil, false, inputs); e == nil {
		t.Error("pending owner wait must not be silently blocked or duplicated")
	}
	waitEntered := make(chan struct{})
	inputs.AwaitTokenCount = func(ctx context.Context, done <-chan struct{}) error {
		close(waitEntered)
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	second := make(chan error, 1)
	go func() {
		plan, e := service.Plan(ctx, "fixture-model", tools, nil, false, inputs)
		if e == nil && !plan.Enabled {
			t.Error("wrong cached decision")
		}
		second <- e
	}()
	<-waitEntered
	close(release)
	if e := <-first; e != nil {
		t.Fatal(e)
	}
	if e := <-second; e != nil {
		t.Fatal(e)
	}
	if count.Load() != 1 {
		t.Fatal("count promise not shared", count.Load())
	}
}
