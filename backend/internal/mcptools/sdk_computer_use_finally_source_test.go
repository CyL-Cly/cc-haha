package mcptools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func TestInactiveComputerUseFinallyMatchesActualOriginalRuntimeReactions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-finally-reaction.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CU_FINALLY_REACTION=1", "BUN_OPTIONS=--no-env-file"}
	input, _ := json.Marshal(map[string]string{"home": home})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, string(output), stderr.String())
	}
	var source struct {
		Rows []struct {
			Profile        string
			CompletionTick int
			SameError      bool
		}
		FullCleanup struct{ Reads, Timers, Cleared int }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != 3 || !reflect.DeepEqual(source.FullCleanup, struct{ Reads, Timers, Cleared int }{1, 1, 1}) {
		t.Fatal("actual original cleanup/finally profile", err, string(output))
	}
	for _, row := range source.Rows {
		t.Run(row.Profile, func(t *testing.T) {
			s, refs := inactiveComputerUseService(t)
			original, thrown := errors.New("original cleanup rejection"), errors.New("finally callback failure")
			err := s.options.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				parent, e := ownerasync.NewOwnerFuture[struct{}](s.options.Owner)
				if e != nil {
					return e
				}
				if row.Profile == "rejected" {
					e = parent.Reject(scope, original)
				} else {
					e = parent.Resolve(scope, struct{}{})
				}
				if e != nil {
					return e
				}
				tick, completionTick, called, completed := 0, -1, 0, false
				result, e := s.computerUseFinally(scope, parent, func(*cooperative.Scope) error {
					called++
					if row.Profile == "throwing" {
						return thrown
					}
					return nil
				})
				if e != nil {
					return e
				}
				var observed error
				_, e = result.Then(scope, func(_ *cooperative.Scope, _ struct{}, failure error) error {
					completionTick, completed, observed = tick, true, failure
					return nil
				})
				if e != nil {
					return e
				}
				var probe func(*cooperative.Scope) error
				probe = func(*cooperative.Scope) error {
					tick++
					if !completed && tick < 32 {
						_, e := s.options.Owner.Microtask(cooperative.Read, probe)
						return e
					}
					return nil
				}
				if _, e = s.options.Owner.Microtask(cooperative.Read, probe); e != nil {
					return e
				}
				_, failure := result.Await(scope)
				want := error(nil)
				if row.Profile == "rejected" {
					want = original
				} else if row.Profile == "throwing" {
					want = thrown
				}
				if called != 1 || completionTick != row.CompletionTick || failure != want || observed != want || want != nil && !row.SameError {
					t.Errorf("actual finally reaction: Go tick=%d calls=%d failure=%v observation=%v; Source tick=%d sameError=%v", completionTick, called, failure, observed, row.CompletionTick, row.SameError)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if count, _ := refs.Snapshot(); count != 0 {
				t.Fatal("pure finally allocated native work", count)
			}
		})
	}
}
