package mcptools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func TestInactiveComputerUseWarmDefaultCleanupMatchesActualOriginalModuleReactions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	if e := os.MkdirAll(filepath.Join(home, "tmp"), 0700); e != nil {
		t.Fatal(e)
	}
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-warm-module.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CU_WARM_MODULE=1", "BUN_OPTIONS=--no-env-file"}
	command.Stdin = bytes.NewBufferString(`{"home":` + string(raw(home)) + `}`)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, e := command.Output()
	if e != nil {
		t.Fatal(e, string(output), stderr.String())
	}
	var source struct {
		Rows []struct {
			Profile          string
			CompletionTick   int
			OriginalIdentity bool
			SameNamespace    bool
		}
		DistinctImportPromises, SameNamespace, Inactive bool
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Rows) != 5 || !source.DistinctImportPromises || !source.SameNamespace || !source.Inactive {
		t.Fatal("actual warm module profile", e, string(output))
	}
	t.Logf("actual original warmed module/cleanup rows: %s", output)
	for _, row := range source.Rows {
		t.Run(row.Profile, func(t *testing.T) {
			if !row.OriginalIdentity {
				t.Fatal("Source import/caller Promise identity changed")
			}
			s, refs := inactiveComputerUseService(t)
			e := s.options.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				// Source awaited all three real modules before measuring these
				// warm rows. Perform the same actual setup on this fresh Go cache.
				for key := computerUseCleanupModule; key < computerUseModuleCount; key++ {
					first, failure := s.importComputerUseModule(scope, key)
					if failure != nil {
						return failure
					}
					if _, failure = first.Await(scope); failure != nil {
						return failure
					}
				}
				var result *ownerasync.OwnerFuture[struct{}]
				var e error
				if row.Profile == "default-caller" {
					result, e = s.beginInactiveComputerUseTurn(scope)
				} else if row.Profile == "cleanup" {
					result, e = s.inactiveComputerUseAfterTurn(scope)
				} else {
					key := computerUseModuleKey(row.Profile[len(row.Profile)-1] - '0')
					module, failure := s.importComputerUseModule(scope, key)
					if failure != nil {
						return failure
					}
					return checkComputerUseWarmReaction(t, scope, s, module, row.CompletionTick, func(namespace *computerUseModuleNamespace) {
						if !row.SameNamespace || namespace != s.inactiveComputerUse.modules[key].record.namespace {
							t.Error("actual import namespace cache identity lost")
						}
					})
				}
				if e != nil {
					return e
				}
				return checkComputerUseWarmReaction(t, scope, s, result, row.CompletionTick, func(struct{}) {})
			})
			if e != nil {
				t.Fatal(e)
			}
			if count, _ := refs.Snapshot(); count != 0 {
				t.Fatal("warm module allocated native I/O", count)
			}
		})
	}
}

func checkComputerUseWarmReaction[T any](t *testing.T, scope *cooperative.Scope, s *Service, result *ownerasync.OwnerFuture[T], expected int, inspect func(T)) error {
	t.Helper()
	tick, completion, done := 0, -1, false
	_, e := result.Then(scope, func(_ *cooperative.Scope, value T, failure error) error {
		completion, done = tick, true
		if failure == nil {
			inspect(value)
		}
		return failure
	})
	if e != nil {
		return e
	}
	var probe func(*cooperative.Scope) error
	probe = func(*cooperative.Scope) error {
		tick++
		if !done && tick < 256 {
			_, e := s.options.Owner.Microtask(cooperative.Read, probe)
			return e
		}
		return nil
	}
	if _, e = s.options.Owner.Microtask(cooperative.Read, probe); e != nil {
		return e
	}
	if _, e = result.Await(scope); e != nil {
		return e
	}
	if completion != expected {
		t.Errorf("actual warm original reaction Go=%d Source=%d", completion, expected)
	}
	return nil
}
