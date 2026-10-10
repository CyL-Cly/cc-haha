package builtintools

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionplans"
)

type blockingPlanFiles struct {
	sessionplans.NativeFiles
	started, release chan struct{}
	failure          error
}

func (f blockingPlanFiles) WriteFile(path string, data []byte, mode fs.FileMode) error {
	close(f.started)
	<-f.release
	if f.failure != nil {
		return f.failure
	}
	return f.NativeFiles.WriteFile(path, data, mode)
}

// The one replacement is the OS write admission, while the actual native
// reference/Promise/owner continuation and file operation remain in use.
func TestPlanEditedWriteYieldsOwnerAndModeChangesAfterNativeCompletion(t *testing.T) {
	for _, row := range []struct {
		name    string
		failure error
		cancel  bool
	}{{name: "written"}, {name: "failed write is caught", failure: errors.New("physical edit rejected")}, {name: "cancel drains already started write", cancel: true}} {
		t.Run(row.name, func(t *testing.T) {
			failure := row.failure
			s, owner, home, permission := fixture(t)
			permission.Set("mode", quote("plan"))
			state, e := bootstrapstate.New(owner, "same-session", home, func() (string, error) { return "next", nil })
			if e != nil {
				t.Fatal(e)
			}
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			settings := protocol.NewJSONObject()
			logged := 0
			plans, e := sessionplans.New(sessionplans.Options{Bootstrap: state, ConfigDir: filepath.Join(home, ".claude"), CWD: home, InitialSettings: func() (*protocol.JSONObject, error) { return &settings, nil }, Files: blockingPlanFiles{started: started, release: release, failure: failure}, WordSlug: func() (string, error) { return "owned-plan", nil }, OnError: func(error) { logged++ }})
			if e != nil {
				t.Fatal(e)
			}
			exited := 0
			s.options.PlanMode = &PlanModeBindings{Plans: plans, CheckInactive: func(*cooperative.Scope) error { return nil }, Enter: func(*cooperative.Scope) error { return nil }, Exit: func(*cooperative.Scope) error { exited++; permission.Set("mode", quote("default")); return nil }, InterviewPhase: func() (bool, error) { return false, nil }, HasTaskTool: func(*cooperative.Scope) (bool, error) { return false, nil }}
			var pending *ownerasync.OwnerFuture[queryloop.ToolCallResult]
			var path string
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			toolContext, cancelTool := context.WithCancel(context.Background())
			defer cancelTool()
			if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
				var e error
				path, e = plans.Path(scope, "")
				if e != nil {
					return e
				}
				if e := os.WriteFile(path, []byte("original"), 0600); e != nil {
					return e
				}
				pending, e = s.startPlanMode(toolContext, "ExitPlanMode", input(`{"plan":"edited"}`), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("physical write did not start")
			}
			if row.cancel {
				cancelTool()
			}
			if e := owner.Run(ctx, func(*cooperative.Scope) error {
				if exited != 0 || permission.String("mode") != "plan" || logged != 0 {
					t.Fatal("mode/logging happened before awaited write")
				}
				select {
				case <-pending.Done():
					t.Fatal("edited tool completed before native write")
				default:
				}
				data, e := os.ReadFile(path)
				if e != nil || string(data) != "original" {
					t.Fatal("write crossed admission", string(data), e)
				}
				return nil
			}); e != nil {
				t.Fatal("owner blocked by native write", e)
			}
			once.Do(func() { close(release) })
			if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
				result, e := pending.Await(scope)
				if e != nil {
					return e
				}
				var data protocol.JSONObject
				_ = data.UnmarshalJSON(result.Data)
				if data.String("plan") != "edited" || string(data.Get("planWasEdited")) != "true" || exited != 1 || permission.String("mode") != "default" {
					t.Fatal("Source completion/result differs", data, exited)
				}
				want := "edited"
				wantErrors := 0
				if failure != nil {
					want = "original"
					wantErrors = 1
				}
				actual, e := os.ReadFile(path)
				if e != nil || string(actual) != want || logged != wantErrors {
					t.Fatal("Source caught write failure differs", string(actual), logged, e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
