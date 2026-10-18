package nativemodule

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

type actualImportObservation struct {
	Microtasks            int
	Identity              bool
	DistinctImportPromise bool
	SameNamespace         bool
}

func TestPrelinkedWarmImportMatchesActualSourcePluginModule(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun required")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/prelinked-module-import.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PRELINKED_MODULE_IMPORT=1", "BUN_OPTIONS=--no-env-file"}
	payload, err := json.Marshal(map[string]string{"home": home})
	if err != nil {
		t.Fatal(err)
	}
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("actual original module setup/import", err, string(output), stderr.String())
	}
	var source struct {
		OriginalIdentity, ExportedActualHandler bool
		NamespaceOwnKeys                        []string
		Rows                                    []actualImportObservation
		Caps                                    struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != 3 || !source.OriginalIdentity || !source.ExportedActualHandler || len(source.NamespaceOwnKeys) == 0 || len(source.Caps.Blocked) != 0 || stderr.Len() != 0 {
		t.Fatal("original module namespace/Promise/capability mismatch", err, string(output), stderr.String())
	}
	t.Logf("whole original main/plugins warm import: %s", output)
	owner := cooperative.New()
	defer owner.Close()
	if err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry := linkedTestEntry(t, owner, scope)
		fetch, load, evaluation := entry.fetch, entry.load, entry.evaluation
		first, err := entry.Import(scope)
		if err != nil {
			return err
		}
		namespace, err := first.Await(scope)
		if err != nil || namespace != entry.namespace {
			return fmt.Errorf("initial actual linked namespace: %v", err)
		}
		seen := map[*ownerasync.PromiseIdentity]bool{first.Identity(): true}
		for index, row := range source.Rows {
			if !row.Identity || !row.DistinctImportPromise || !row.SameNamespace || row.Microtasks <= 0 {
				return fmt.Errorf("Source warm import %d changed identity: %+v", index, row)
			}
			pending, err := entry.Import(scope)
			if err != nil {
				return err
			}
			if pending == first || seen[pending.Identity()] {
				return fmt.Errorf("warm import %d reused public Promise identity", index)
			}
			seen[pending.Identity()] = true
			if err = observeActualPrelinkedImport(scope, owner, pending, namespace, row.Microtasks); err != nil {
				return fmt.Errorf("warm import %d: %w", index, err)
			}
			if entry.fetch != fetch || entry.load != load || entry.evaluation != evaluation {
				return fmt.Errorf("warm import %d replaced cached load/evaluation arms", index)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// This is the same recursively queued observer as the original fixture. Its
// ordinal is evidence about Promise reactions, never production scheduling.
func observeActualPrelinkedImport(scope *cooperative.Scope, owner *cooperative.Owner, pending *ownerasync.OwnerFuture[*testNamespace], namespace *testNamespace, expected int) error {
	count, completed := 0, false
	var returned *testNamespace
	var rejected error
	_, err := pending.Then(scope, func(_ *cooperative.Scope, value *testNamespace, failure error) error {
		returned, rejected, completed = value, failure, true
		return nil
	})
	if err != nil {
		return err
	}
	var probe func(*cooperative.Scope) error
	probe = func(*cooperative.Scope) error {
		count++
		if !completed && count < 1000 {
			_, err := owner.Microtask(cooperative.Read, probe)
			return err
		}
		return nil
	}
	if _, err = owner.Microtask(cooperative.Read, probe); err != nil {
		return err
	}
	if _, err = pending.Await(scope); err != nil {
		return err
	}
	if !completed || rejected != nil || returned != namespace || count != expected {
		return fmt.Errorf("actual import observer Go=%d Source=%d completed=%v sameNamespace=%v rejected=%v", count, expected, completed, returned == namespace, rejected)
	}
	return nil
}

func TestPrelinkedActualImportWaitsForBothCachedLoadAndEvaluationArms(t *testing.T) {
	for _, arm := range []string{"host-load", "evaluation"} {
		for _, rejected := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rejected=%v", arm, rejected), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				owner := cooperative.New()
				defer owner.Close()
				failure := errors.New("actual pending module arm failure")
				var entry *Entry[string, *testNamespace]
				var pending *ownerasync.OwnerFuture[*testNamespace]
				if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
					entry = linkedTestEntry(t, owner, scope)
					var err error
					if arm == "host-load" {
						entry.load, err = ownerasync.NewOwnerFuture[*Entry[string, *testNamespace]](owner)
					} else {
						entry.evaluation, err = ownerasync.NewOwnerFuture[struct{}](owner)
					}
					if err != nil {
						return err
					}
					pending, err = entry.Import(scope)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				// The ordinary Owner Read follows its queued Promise reactions. It
				// observes the pending import without injecting a timer or reaction
				// count, then settles the one deliberately held actual cache arm.
				receipt, err := owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
					select {
					case <-pending.Done():
						return errors.New("one successful cache arm released the actual import")
					default:
					}
					var settleErr error
					if arm == "host-load" {
						if rejected {
							settleErr = entry.load.Reject(scope, failure)
						} else {
							settleErr = entry.load.Resolve(scope, entry)
						}
					} else if rejected {
						settleErr = entry.evaluation.Reject(scope, failure)
					} else {
						settleErr = entry.evaluation.Resolve(scope, struct{}{})
					}
					if settleErr != nil {
						return settleErr
					}
					value, result := pending.Await(scope)
					if rejected {
						if result != failure {
							return fmt.Errorf("outer import changed original rejection: %v", result)
						}
					} else if result != nil || value != entry.namespace {
						return fmt.Errorf("outer import changed namespace: %v", result)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if err = receipt.Wait(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
