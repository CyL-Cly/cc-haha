package sdkhost_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// The old fixture must run as an isolated go test process: after its private
// subtest scope really disappears, releasing the admitted Stat reaches the
// original disposable-file guard and panics. No guard or filesystem result is
// replaced. The current fixture releases the same work at administrative Join,
// after its immutable business snapshot and before environment/home teardown.
func TestRunNativeFixtureRetiresHeldTranscriptStatBeforePrivateScopeCleanup(t *testing.T) {
	entered := make(chan string, 1)
	snapshot := make(chan struct{})
	joining := make(chan struct{})
	scopeCleaned := make(chan struct{})
	releaseStat := make(chan struct{})
	actorDone := make(chan error, 1)
	home := make(chan string, 1)
	record := make(chan *sessioncontroller.RecordFuture, 1)
	var enabled atomic.Bool
	var holdOnce, snapshotOnce, joinOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseStat) }) }
	defer release()

	go func() {
		select {
		case <-joining:
			select {
			case <-snapshot:
			default:
				release()
				actorDone <- fmt.Errorf("administrative Join preceded retained business snapshot")
				return
			}
			t.Log("SDKHOST_ADMIN_JOIN_RELEASE_BEFORE_PRIVATE_SCOPE_CLEANUP")
			release()
			actorDone <- nil
		case <-scopeCleaned:
			var privateHome string
			select {
			case privateHome = <-home:
			default:
				release()
				actorDone <- fmt.Errorf("setup failed before actual serial SDK admission published private home")
				return
			}
			if _, err := os.Lstat(privateHome); !os.IsNotExist(err) {
				release()
				actorDone <- fmt.Errorf("old-view cleanup precondition: private home still exists: %v", err)
				return
			}
			t.Log("SDKHOST_RELEASE_AFTER_PRIVATE_SCOPE_CLEANUP: original admitted Stat now resumes")
			t.Error("fixture returned and removed private scope before admitted transcript Stat completed")
			release()
			// Leave the outer test waiting while the original worker resumes its
			// unchanged guard. A premature test-process exit must not hide panic.
			return
		case <-time.After(10 * time.Second):
			release()
			actorDone <- fmt.Errorf("administrative watchdog: neither Join nor fixture-scope cleanup observed")
		}
	}()

	t.Run("actual-held-stat-private-scope", func(child *testing.T) {
		_, result := originalBoundary(child, nativeControls{
			BeforeTranscriptStat: func(path string) {
				if !enabled.Load() {
					return
				}
				holdOnce.Do(func() {
					t.Logf("SDKHOST_HELD_STAT_ENTERED: %s", path)
					entered <- path
					<-releaseStat
				})
			},
			SnapshotObserved: func() {
				snapshotOnce.Do(func() {
					t.Log("SDKHOST_SNAPSHOT_RETAINED_WITH_HELD_STAT")
					close(snapshot)
				})
			},
			AdministrativeJoinStarted: func() { joinOnce.Do(func() { close(joining) }) },
			Run: func(options sdkhost.Options, _ *sdkhost.Host, inputs []protocol.JSONObject) error {
				home <- options.CWD
				serial, err := sessioncontroller.NewSerialASK(options.Controller, options.Owner)
				if err != nil {
					return err
				}
				var future *sessioncontroller.RecordFuture
				err = options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					accepted, err := serial.Begin(scope, inputs[0])
					if err != nil {
						return err
					}
					if accepted == nil {
						return fmt.Errorf("actual serial SDK input was not admitted")
					}
					enabled.Store(true)
					future, err = serial.BeginRecord(scope, serial.History())
					if err != nil {
						return err
					}
					return serial.Finally(scope)
				})
				if err != nil {
					return err
				}
				record <- future
				select {
				case <-entered:
					return nil
				case <-time.After(5 * time.Second):
					return fmt.Errorf("administrative watchdog: actual Record discard did not enter Stat")
				}
			},
		})
		if len(result["requests"].([]string)) != 0 {
			child.Fatal("fixture-retirement regression unexpectedly queried a model")
		}
	})
	// t.Run returns only after the child's Setenv/Chdir and TempDir cleanups.
	// In the old view this is the release fence for the original panic, rather
	// than a sleep or a fabricated error from a replacement filesystem.
	close(scopeCleaned)
	select {
	case err := <-actorDone:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("administrative watchdog awaiting held-Stat actor termination")
	}
	select {
	case future := <-record:
		if future == nil {
			t.Error("fixture published no actual Record future")
		}
		// Administrative resource retirement must not invent a requirement to
		// await a pure outer Record promise. The regression's boundary is the
		// real held filesystem worker versus private fixture cleanup.
	default:
		t.Error("fixture did not publish its actual admitted Record")
	}
}
