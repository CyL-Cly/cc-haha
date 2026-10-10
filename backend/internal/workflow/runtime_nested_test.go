package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNestedWorkflowPhysicalAgentAdmissionsSharePoolAndDrainOnCancel(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelRun), func(t *testing.T) {
			root, e := CompileBody(utf16.Encode([]rune(`return await parallel([()=>workflow('child'),()=>agent('parent')])`)))
			if e != nil {
				t.Fatal(e)
			}
			child, e := CompileBody(utf16.Encode([]rune(`return await parallel([()=>agent('child-first'),()=>agent('child-second')])`)))
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var mu sync.Mutex
			active, maximum, finished := 0, 0, 0
			indexes := []int{}
			started := make(chan string, 3)
			release := make(chan struct{})
			done := make(chan ExecutionOutcome, 1)
			options := ExecutionOptions{Program: root, RunID: "wf_shared", WorkflowName: "parent", Model: "fixture", Concurrency: 2, Now: func() float64 { return 0 }, GetTurnSpent: func() float64 { return 0 }, Emit: func(protocol.JSONObject) error { return nil }, PrepareNested: func(context.Context, json.RawMessage) (PreparedScript, error) {
				return PreparedScript{Program: child, Meta: protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoted("child")})}, nil
			}, RunAgent: func(ctx context.Context, request AgentExecution) (AgentOutcome, error) {
				mu.Lock()
				active++
				if active > maximum {
					maximum = active
				}
				indexes = append(indexes, request.Index)
				mu.Unlock()
				started <- request.Prompt
				defer func() { mu.Lock(); active--; finished++; mu.Unlock() }()
				select {
				case <-release:
					return AgentOutcome{AgentID: fmt.Sprint(request.Index), Value: quoted(request.Prompt)}, nil
				case <-ctx.Done():
					return AgentOutcome{}, context.Cause(ctx)
				}
			}}
			go func() {
				out, e := Execute(ctx, options)
				if e != nil {
					out.Error = e.Error()
				}
				done <- out
			}()
			for i := 0; i < 2; i++ {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("shared slot dispatch blocked")
				}
			}
			mu.Lock()
			if maximum != 2 {
				t.Errorf("separate nested pool allowed %d physical Agents", maximum)
			}
			mu.Unlock()
			if cancelRun {
				cancel()
			} else {
				close(release)
			}
			var result ExecutionOutcome
			select {
			case result = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("actual child callbacks failed to drain")
			}
			mu.Lock()
			defer mu.Unlock()
			if active != 0 || maximum > 2 {
				t.Fatal("shared native slot or canceled worker leaked", active, maximum)
			}
			if !cancelRun {
				sort.Ints(indexes)
				if result.Error != "" || !reflect.DeepEqual(indexes, []int{1, 2, 3}) || finished != 3 || result.AgentCount != 3 {
					t.Fatal(result, indexes, finished)
				}
			} else if result.Error != "Workflow aborted" {
				t.Fatal(result)
			}
		})
	}
}
