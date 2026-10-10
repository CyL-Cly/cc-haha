package concurrentsessions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

type operation struct {
	kind, path string
	mode       fs.FileMode
	data       []byte
	void       *VoidFuture
	read       *ownerasync.OwnerFuture[[]byte]
	dir        *ownerasync.OwnerFuture[[]string]
}

type controlledFiles struct {
	owner *cooperative.Owner
	ops   []*operation
}

func (files *controlledFiles) start(kind, path string, mode fs.FileMode, data []byte) (*VoidFuture, error) {
	future, err := ownerasync.NewOwnerFuture[struct{}](files.owner)
	files.ops = append(files.ops, &operation{kind: kind, path: path, mode: mode, data: append([]byte(nil), data...), void: future})
	return future, err
}
func (files *controlledFiles) Mkdir(_ *cooperative.Scope, path string, mode fs.FileMode) (*VoidFuture, error) {
	return files.start("mkdir", path, mode, nil)
}
func (files *controlledFiles) Chmod(_ *cooperative.Scope, path string, mode fs.FileMode) (*VoidFuture, error) {
	return files.start("chmod", path, mode, nil)
}
func (files *controlledFiles) WriteFile(_ *cooperative.Scope, path string, data []byte, mode fs.FileMode) (*VoidFuture, error) {
	return files.start("write", path, mode, data)
}
func (files *controlledFiles) Unlink(_ *cooperative.Scope, path string) (*VoidFuture, error) {
	return files.start("unlink", path, 0, nil)
}
func (files *controlledFiles) ReadFile(_ *cooperative.Scope, path string) (*ownerasync.OwnerFuture[[]byte], error) {
	future, err := ownerasync.NewOwnerFuture[[]byte](files.owner)
	files.ops = append(files.ops, &operation{kind: "read", path: path, read: future})
	return future, err
}
func (files *controlledFiles) ReadDir(_ *cooperative.Scope, path string) (*ownerasync.OwnerFuture[[]string], error) {
	future, err := ownerasync.NewOwnerFuture[[]string](files.owner)
	files.ops = append(files.ops, &operation{kind: "readdir", path: path, dir: future})
	return future, err
}

// This observer acknowledges delivery of the operation's already-registered
// continuation. It does not alter the Service, drive a fixed await count or
// resolve another native operation without that operation's explicit input.
func deliver[T any](scope *cooperative.Scope, future *ownerasync.OwnerFuture[T], value T, failure error) error {
	if err := settle(scope, future, value, failure); err != nil {
		return err
	}
	ack, err := future.Then(scope, func(*cooperative.Scope, T, error) error { return nil })
	if err != nil {
		return err
	}
	_, err = ack.Await(scope)
	return err
}

type testHost struct {
	home, sid, cwd, platform string
	now                      int64
	agent                    *string
	env                      map[string]string
	listeners                []func(*cooperative.Scope, string) error
	logs                     []string
	probes                   []float64
	live                     map[float64]bool
}

func (host *testHost) bindings(bg, uds bool) Host {
	return Host{
		PID: 42, BG: bg, UDS: uds,
		ConfigHome: func() string { return host.home }, SessionID: func() string { return host.sid }, OriginalCWD: func() string { return host.cwd },
		NowMilliseconds: func() int64 { return host.now }, Environment: func(key string) (string, bool) { value, ok := host.env[key]; return value, ok },
		AgentID: func() *string { return host.agent },
		SubscribeSessionSwitch: func(_ *cooperative.Scope, callback func(*cooperative.Scope, string) error) error {
			host.listeners = append(host.listeners, callback)
			return nil
		},
		ProcessRunning: func(pid float64) bool { host.probes = append(host.probes, pid); return host.live[pid] }, Platform: func() string { return host.platform },
		Debug: func(message string) { host.logs = append(host.logs, message) }, ErrorMessage: func(err error) string { return err.Error() },
	}
}

func withService(t *testing.T, bg, uds bool, body func(*cooperative.Scope, *Service, *controlledFiles, *testHost, *runtimecleanup.Registry) error) {
	t.Helper()
	owner := cooperative.New()
	defer owner.Close()
	cleanup, _ := runtimecleanup.New(owner)
	files := &controlledFiles{owner: owner}
	host := &testHost{home: "/private/initial", sid: "initial-sid", cwd: "/private/work", now: 10, platform: "macos", env: map[string]string{}, live: map[float64]bool{}}
	service, err := New(owner, cleanup, files, host.bindings(bg, uds))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error { return body(scope, service, files, host, cleanup) }); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationCapturesKindAndPathButReadsBootstrapAfterChmod(t *testing.T) {
	withService(t, true, true, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, cleanup *runtimecleanup.Registry) error {
		host.env["CLAUDE_CODE_SESSION_KIND"] = "bg"
		registered, err := service.RegisterSession(scope)
		if err != nil || len(files.ops) != 1 || files.ops[0].kind != "mkdir" || len(host.listeners) != 0 {
			return fmt.Errorf("registration initial effects: %v %v", files.ops, err)
		}
		cleaned, err := cleanup.Start(scope)
		if err != nil || len(files.ops) != 2 || files.ops[1].kind != "unlink" {
			return fmt.Errorf("cleanup must already be registered: %v %v", files.ops, err)
		}
		if err := deliver(scope, files.ops[1].void, struct{}{}, fs.ErrNotExist); err != nil {
			return err
		}
		if _, err := cleaned.Await(scope); err != nil {
			return err
		}
		host.home, host.sid, host.cwd, host.now = "/private/later", "new-sid", "/new-work", 20
		host.env["CLAUDE_CODE_SESSION_KIND"] = "daemon"
		host.env["CLAUDE_CODE_ENTRYPOINT"] = "sdk"
		host.env["CLAUDE_CODE_SESSION_NAME"] = "名字🚀"
		host.env["CLAUDE_CODE_MESSAGING_SOCKET"] = "/private/socket"
		if err := deliver(scope, files.ops[0].void, struct{}{}, nil); err != nil {
			return err
		}
		if len(files.ops) != 3 || files.ops[2].kind != "chmod" || len(host.listeners) != 0 {
			return errors.New("chmod starts only after mkdir, without an early SID listener")
		}
		if err := deliver(scope, files.ops[2].void, struct{}{}, nil); err != nil {
			return err
		}
		write := files.ops[3]
		want := `{"pid":42,"sessionId":"new-sid","cwd":"/new-work","startedAt":20,"kind":"bg","entrypoint":"sdk","messagingSocketPath":"/private/socket","name":"名字🚀"}`
		if write.kind != "write" || write.path != "/private/initial/sessions/42.json" || write.mode != 0666 || string(write.data) != want || len(host.listeners) != 0 {
			return fmt.Errorf("write captured/live fields: %#v %s", write, write.data)
		}
		if err := deliver(scope, write.void, struct{}{}, nil); err != nil {
			return err
		}
		ok, err := registered.Await(scope)
		if err != nil || !ok || len(host.listeners) != 1 {
			return fmt.Errorf("post-write registration: %v %v listeners=%d", ok, err, len(host.listeners))
		}
		if err := host.listeners[0](scope, "switched"); err != nil {
			return err
		}
		if files.ops[4].kind != "read" || files.ops[4].path != "/private/later/sessions/42.json" {
			return errors.New("SID updater must read current config path, without joining in the signal")
		}
		return nil
	})
}

func TestTeammateEmptyAgentIDSkipsRegistrationAndCleanup(t *testing.T) {
	withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, cleanup *runtimecleanup.Registry) error {
		empty := ""
		host.agent = &empty
		future, err := service.RegisterSession(scope)
		if err != nil {
			return err
		}
		ok, err := future.Await(scope)
		if err != nil || ok {
			return fmt.Errorf("teammate: %v %v", ok, err)
		}
		cleaned, _ := cleanup.Start(scope)
		_, err = cleaned.Await(scope)
		if len(files.ops) != 0 || len(host.listeners) != 0 {
			return errors.New("teammate must have no FS/cleanup/SID effects")
		}
		return err
	})
}

func TestRegistrationNativeFailuresAreBestEffortAndRetainCleanup(t *testing.T) {
	for failedStep := 0; failedStep < 3; failedStep++ {
		t.Run(fmt.Sprint(failedStep), func(t *testing.T) {
			withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, cleanup *runtimecleanup.Registry) error {
				future, _ := service.RegisterSession(scope)
				for index := 0; index <= failedStep; index++ {
					var failure error
					if index == failedStep {
						failure = syscall.EACCES
					}
					if err := deliver(scope, files.ops[index].void, struct{}{}, failure); err != nil {
						return err
					}
				}
				ok, err := future.Await(scope)
				if err != nil || ok || len(host.logs) != 1 || len(host.listeners) != 0 {
					return fmt.Errorf("failure contract: %v/%v logs=%v", ok, err, host.logs)
				}
				cleaned, _ := cleanup.Start(scope)
				last := files.ops[len(files.ops)-1]
				if last.kind != "unlink" {
					return errors.New("failed registration lost cleanup")
				}
				_ = deliver(scope, last.void, struct{}{}, syscall.EPERM)
				_, err = cleaned.Await(scope)
				return err
			})
		})
	}
}

func TestPlainJSONSpreadAndStringifyPreserveJavaScriptFields(t *testing.T) {
	for _, fixture := range []struct{ raw, want string }{
		{`null`, `{"bridgeSessionId":null}`}, {`17`, `{"bridgeSessionId":null}`}, {`false`, `{"bridgeSessionId":null}`},
		{`[1,"x",null]`, `{"0":1,"1":"x","2":null,"bridgeSessionId":null}`},
		{`"中文🚀\ud800"`, `{"0":"中","1":"文","2":"\ud83d","3":"\ude80","4":"\ud800","bridgeSessionId":null}`},
		{`{"b":1,"2":2,"a":0,"b":9007199254740993,"bridgeSessionId":"old","name":"keep","__proto__":7}`, `{"2":2,"b":9007199254740992,"a":0,"bridgeSessionId":null,"name":"keep","__proto__":7}`},
	} {
		t.Run(fixture.raw, func(t *testing.T) {
			withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, _ *testHost, _ *runtimecleanup.Registry) error {
				future, _ := service.UpdateSessionBridgeID(scope, nil)
				if err := deliver(scope, files.ops[0].read, []byte(fixture.raw), nil); err != nil {
					return err
				}
				if len(files.ops) != 2 || string(files.ops[1].data) != fixture.want {
					return fmt.Errorf("spread wanted %s got %s", fixture.want, files.ops[1].data)
				}
				_ = deliver(scope, files.ops[1].void, struct{}{}, nil)
				_, err := future.Await(scope)
				return err
			})
		})
	}
}

func TestUpdateGatesAndActivityTimeAreSampledBeforeRead(t *testing.T) {
	withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, _ *runtimecleanup.Registry) error {
		empty := ""
		for _, name := range []*string{nil, &empty} {
			future, _ := service.UpdateSessionName(scope, name)
			if _, err := future.Await(scope); err != nil {
				return err
			}
		}
		future, _ := service.UpdateSessionActivity(scope, protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: quoted("busy")}))
		_, err := future.Await(scope)
		if len(files.ops) != 0 {
			return errors.New("disabled activity/empty names touched FS")
		}
		host.env["CLAUDE_CODE_SESSION_KIND"] = "bg"
		if bg, _ := service.IsBGSession(scope); bg {
			return errors.New("BG env must obey compile feature")
		}
		return err
	})
	withService(t, true, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, _ *runtimecleanup.Registry) error {
		patch := protocol.NewJSONObject(protocol.JSONField{Name: "status"}, protocol.JSONField{Name: "waitingFor", Value: quoted("permission")}, protocol.JSONField{Name: "updatedAt", Value: []byte("999")})
		future, _ := service.UpdateSessionActivity(scope, patch)
		host.now = 80
		patch.Set("waitingFor", quoted("mutated"))
		_ = deliver(scope, files.ops[0].read, []byte(`{"status":"busy","name":"keep"}`), nil)
		if string(files.ops[1].data) != `{"name":"keep","waitingFor":"permission","updatedAt":10}` {
			return fmt.Errorf("activity own undefined/time/spread: %s", files.ops[1].data)
		}
		_ = deliver(scope, files.ops[1].void, struct{}{}, nil)
		_, err := future.Await(scope)
		return err
	})
}

func TestMalformedMissingAndWriteFailureAreBestEffort(t *testing.T) {
	for _, input := range []struct {
		raw               string
		readErr, writeErr error
	}{
		{raw: `{"broken":`}, {raw: "\ufeff{}"}, {readErr: fs.ErrNotExist}, {raw: `{}`, writeErr: syscall.EACCES},
	} {
		t.Run(fmt.Sprintf("%q-%v-%v", input.raw, input.readErr, input.writeErr), func(t *testing.T) {
			withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, _ *runtimecleanup.Registry) error {
				name := "name"
				future, _ := service.UpdateSessionName(scope, &name)
				_ = deliver(scope, files.ops[0].read, []byte(input.raw), input.readErr)
				if input.writeErr != nil {
					_ = deliver(scope, files.ops[1].void, struct{}{}, input.writeErr)
				}
				_, err := future.Await(scope)
				if err != nil || len(host.logs) != 1 || !strings.HasPrefix(host.logs[0], "[concurrentSessions] updatePidFile failed: ") {
					return fmt.Errorf("best effort: %v %v", err, host.logs)
				}
				return nil
			})
		})
	}
}

func TestCountStrictFilenamesDoublePIDsAndUnjoinedStaleRemoval(t *testing.T) {
	withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, _ *runtimecleanup.Registry) error {
		host.live[77] = true
		future, _ := service.CountConcurrentSessions(scope)
		names := []string{"notes.md", "2026-03-14_notes.md", "42.json", "00042.json", "77.json", "90.json", "1.json", "0.json", "9007199254740993.json", "４２.json", "-7.json", "42.json.bak", strings.Repeat("9", 400) + ".json"}
		if err := deliver(scope, files.ops[0].dir, names, nil); err != nil {
			return err
		}
		count, err := future.Await(scope)
		if err != nil || count != 3 || len(files.ops) != 6 {
			return fmt.Errorf("count returned without unlink wait: %d/%v ops=%d", count, err, len(files.ops))
		}
		if !reflect.DeepEqual(host.probes[:5], []float64{77, 90, 1, 0, 9007199254740992}) || !math.IsInf(host.probes[5], 1) {
			return fmt.Errorf("parseInt double probes: %v", host.probes)
		}
		for _, op := range files.ops[1:] {
			select {
			case <-op.void.Done():
				return errors.New("count joined or resolved stale unlink")
			default:
			}
		}
		return nil
	})
}

func TestWSLDoesNotSweepAndInaccessibleReadDirDoesNotDebug(t *testing.T) {
	withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, _ *runtimecleanup.Registry) error {
		host.platform = "wsl"
		future, _ := service.CountConcurrentSessions(scope)
		_ = deliver(scope, files.ops[0].dir, []string{"42.json", "dead.txt", "99.json"}, nil)
		count, err := future.Await(scope)
		if err != nil || count != 1 || len(files.ops) != 1 {
			return fmt.Errorf("WSL: %d/%v effects=%d", count, err, len(files.ops))
		}
		for _, failure := range []error{fs.ErrNotExist, syscall.EACCES, syscall.EPERM, syscall.ENOTDIR, syscall.ELOOP, syscall.EIO} {
			future, _ := service.CountConcurrentSessions(scope)
			_ = deliver(scope, files.ops[len(files.ops)-1].dir, nil, failure)
			count, err = future.Await(scope)
			if err != nil || count != 0 {
				return fmt.Errorf("readdir error: %d/%v", count, err)
			}
		}
		if len(host.logs) != 1 || !strings.Contains(host.logs[0], syscall.EIO.Error()) {
			return fmt.Errorf("inaccessible debug contract: %v", host.logs)
		}
		return nil
	})
}

func TestOwnerAdministrativeFailureIsNotBusinessFalseOrZero(t *testing.T) {
	withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, _ *runtimecleanup.Registry) error {
		registration, _ := service.RegisterSession(scope)
		_ = deliver(scope, files.ops[0].void, struct{}{}, cooperative.ErrClosed)
		if _, err := registration.Await(scope); !errors.Is(err, cooperative.ErrClosed) {
			return fmt.Errorf("admin registration swallowed: %v", err)
		}
		count, _ := service.CountConcurrentSessions(scope)
		_ = deliver(scope, files.ops[1].dir, nil, cooperative.ErrClosed)
		if _, err := count.Await(scope); !errors.Is(err, cooperative.ErrClosed) || len(host.logs) != 0 {
			return fmt.Errorf("admin count swallowed/logged: %v %v", err, host.logs)
		}
		return nil
	})
}

func TestPublicUpdateHasItsOwnAwaitReaction(t *testing.T) {
	withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, _ *testHost, _ *runtimecleanup.Registry) error {
		name := "new-name"
		outer, _ := service.UpdateSessionName(scope, &name)
		_ = deliver(scope, files.ops[0].read, []byte(`{}`), nil)
		observed := false
		observer, err := files.ops[1].void.Then(scope, func(*cooperative.Scope, struct{}, error) error {
			select {
			case <-outer.Done():
				return errors.New("public await layer collapsed into private write completion")
			default:
				observed = true
				return nil
			}
		})
		if err != nil {
			return err
		}
		_ = files.ops[1].void.Resolve(scope, struct{}{})
		if _, err := observer.Await(scope); err != nil {
			return err
		}
		if !observed {
			return errors.New("write observer did not execute")
		}
		_, err = outer.Await(scope)
		return err
	})
}

func TestConcurrentUpdatesKeepSeparateReadModifyWriteJobs(t *testing.T) {
	withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, _ *testHost, _ *runtimecleanup.Registry) error {
		name, bridge := "new-name", "new-bridge"
		first, _ := service.UpdateSessionName(scope, &name)
		second, _ := service.UpdateSessionBridgeID(scope, &bridge)
		if len(files.ops) != 2 || files.ops[0].kind != "read" || files.ops[1].kind != "read" {
			return errors.New("updates were serialized or coalesced before their original reads")
		}
		original := []byte(`{"name":"old-name","bridgeSessionId":"old-bridge"}`)
		_ = deliver(scope, files.ops[0].read, original, nil)
		_ = deliver(scope, files.ops[1].read, original, nil)
		if string(files.ops[2].data) != `{"name":"new-name","bridgeSessionId":"old-bridge"}` || string(files.ops[3].data) != `{"name":"old-name","bridgeSessionId":"new-bridge"}` {
			return fmt.Errorf("read/modify/write race changed: %s / %s", files.ops[2].data, files.ops[3].data)
		}
		_ = deliver(scope, files.ops[2].void, struct{}{}, nil)
		if _, err := first.Await(scope); err != nil {
			return err
		}
		select {
		case <-second.Done():
			return errors.New("one public update joined or settled its independent peer")
		default:
		}
		_ = deliver(scope, files.ops[3].void, struct{}{}, nil)
		_, err := second.Await(scope)
		return err
	})
}

func TestRepeatedRegistrationCreatesDistinctCleanupAndListeners(t *testing.T) {
	withService(t, false, false, func(scope *cooperative.Scope, service *Service, files *controlledFiles, host *testHost, cleanup *runtimecleanup.Registry) error {
		first, _ := service.RegisterSession(scope)
		second, _ := service.RegisterSession(scope)
		if len(files.ops) != 2 {
			return errors.New("register silently became a singleton")
		}
		cleaned, _ := cleanup.Start(scope)
		if len(files.ops) != 4 || files.ops[2].kind != "unlink" || files.ops[3].kind != "unlink" {
			return errors.New("repeated register did not add two original cleanup identities")
		}
		_ = deliver(scope, files.ops[2].void, struct{}{}, nil)
		_ = deliver(scope, files.ops[3].void, struct{}{}, nil)
		if _, err := cleaned.Await(scope); err != nil {
			return err
		}
		_ = deliver(scope, files.ops[0].void, struct{}{}, nil)
		_ = deliver(scope, files.ops[1].void, struct{}{}, nil)
		_ = deliver(scope, files.ops[4].void, struct{}{}, nil)
		_ = deliver(scope, files.ops[5].void, struct{}{}, nil)
		_ = deliver(scope, files.ops[6].void, struct{}{}, nil)
		_ = deliver(scope, files.ops[7].void, struct{}{}, nil)
		if _, err := first.Await(scope); err != nil {
			return err
		}
		if _, err := second.Await(scope); err != nil {
			return err
		}
		if len(host.listeners) != 2 {
			return fmt.Errorf("listener identities: %d", len(host.listeners))
		}
		return nil
	})
}

func TestForeignScopeCannotReadHostOrStartBusinessIO(t *testing.T) {
	withService(t, true, true, func(_ *cooperative.Scope, service *Service, files *controlledFiles, _ *testHost, _ *runtimecleanup.Registry) error {
		foreign := cooperative.New()
		defer foreign.Close()
		reads := 0
		service.host.ConfigHome = func() string { reads++; return "/forbidden" }
		service.host.NowMilliseconds = func() int64 { reads++; return 0 }
		if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
			for _, call := range []func() error{
				func() error { _, err := service.RegisterSession(scope); return err },
				func() error { _, err := service.CountConcurrentSessions(scope); return err },
				func() error { _, err := service.UpdateSessionName(scope, nil); return err },
				func() error { _, err := service.UpdateSessionBridgeID(scope, nil); return err },
				func() error { _, err := service.UpdateSessionActivity(scope, protocol.NewJSONObject()); return err },
				func() error { _, err := service.IsBGSession(scope); return err },
			} {
				if err := call(); !errors.Is(err, cooperative.ErrRestricted) {
					return fmt.Errorf("foreign scope: %v", err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if reads != 0 || len(files.ops) != 0 {
			return fmt.Errorf("foreign scope leaked effects: reads=%d ops=%d", reads, len(files.ops))
		}
		return nil
	})
}
