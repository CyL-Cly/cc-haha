// Finite original registration/cleanup programs. This executable observes the
// actual OS and the shared bootstrap State; it is not a CLI exit implementation.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/concurrentsessions"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

type original struct {
	ID          string `json:"id"`
	Program     string `json:"program"`
	PostCleanup bool   `json:"postCleanup"`
	Entrypoint  string `json:"entrypoint"`
	Umask       int    `json:"umask"`
}

func uuid() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func writeJSON(path string, value any) error {
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, bytes, 0666)
}
func input() (string, original, error) {
	var p original
	home, root := os.Getenv("HOME"), os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if !filepath.IsAbs(home) || !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(home), "cc-haha-bootstrap-race-") {
		return "", p, errors.New("explicit private bootstrap race HOME required")
	}
	canonical, e := filepath.EvalSymlinks(home)
	if e != nil || canonical != home {
		return "", p, errors.New("canonical HOME required")
	}
	parent, e := filepath.EvalSymlinks(root)
	if e != nil || parent != root || filepath.Dir(home) != root {
		return "", p, errors.New("exact allocation parent required")
	}
	if os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") {
		return "", p, errors.New("exact private config required")
	}
	for _, name := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "input.json")} {
		info, e := os.Lstat(name)
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", p, errors.New("unlinked private input ancestors required")
		}
	}
	raw, e := os.ReadFile(filepath.Join(home, "input.json"))
	if e != nil {
		return "", p, e
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&p); e != nil {
		return "", p, e
	}
	var extra any
	if e = decoder.Decode(&extra); e != io.EOF {
		return "", p, errors.New("single original input required")
	}
	want := "parallel-registration-and-cleanup"
	if p.PostCleanup {
		want += "-post-cleanup"
	}
	if p.Program == "A" {
		want = "await-registration-then-cleanup"
	}
	if (p.Program != "A" && p.Program != "B") || p.ID != want || p.Program == "A" && p.PostCleanup || p.Entrypoint != "native-concurrent-session-fixture" || p.Umask != 18 || os.Getenv("CLAUDE_CODE_ENTRYPOINT") != p.Entrypoint {
		return "", p, errors.New("exact original three-program input required")
	}
	for _, name := range []string{filepath.Join(home, ".claude", "sessions"), filepath.Join(home, "result.json"), filepath.Join(home, "go-administrative-observation.json")} {
		if _, e = os.Lstat(name); !errors.Is(e, os.ErrNotExist) {
			return "", p, errors.New("fresh original targets required")
		}
	}
	return home, p, nil
}

type allResult struct{ Registered bool }

// This is the original two-input Promise.all graph, not serial awaits. Both
// producers have already started; their Then reactions settle one result.
func all(scope *cooperative.Scope, owner *cooperative.Owner, registered *ownerasync.OwnerFuture[bool], cleaned *runtimecleanup.Future) (*ownerasync.OwnerFuture[allResult], error) {
	result, _ := ownerasync.NewOwnerFuture[allResult](owner)
	left := 2
	value := allResult{}
	settle := func(next *cooperative.Scope, failure error) error {
		if failure != nil {
			return result.Reject(next, failure)
		}
		left--
		if left == 0 {
			return result.Resolve(next, value)
		}
		return nil
	}
	if _, e := registered.Then(scope, func(next *cooperative.Scope, v bool, e error) error { value.Registered = v; return settle(next, e) }); e != nil {
		return nil, e
	}
	if _, e := cleaned.Then(scope, func(next *cooperative.Scope, _ struct{}, e error) error { return settle(next, e) }); e != nil {
		return nil, e
	}
	return result, nil
}

func run() error {
	home, p, e := input()
	if e != nil {
		return e
	}
	syscall.Umask(p.Umask)
	owner := cooperative.New()
	stateID, e := uuid()
	if e != nil {
		owner.Close()
		return e
	}
	cwd, e := os.Getwd()
	if e != nil {
		owner.Close()
		return e
	}
	state, e := bootstrapstate.New(owner, stateID, cwd, uuid)
	if e != nil {
		owner.Close()
		return e
	}
	cleanup, e := runtimecleanup.New(owner)
	if e != nil {
		owner.Close()
		return e
	}
	files, e := concurrentsessions.NewNativeFileSystem(owner)
	if e != nil {
		owner.Close()
		return e
	}
	debugLogs := []string{}
	host := concurrentsessions.Host{
		PID: os.Getpid(), ConfigHome: func() string { return os.Getenv("CLAUDE_CONFIG_DIR") },
		SessionID: state.SessionID, OriginalCWD: func() string { return state.Snapshot().OriginalCWD }, NowMilliseconds: func() int64 { return time.Now().UnixMilli() },
		Environment: os.LookupEnv, AgentID: func() *string { return nil },
		SubscribeSessionSwitch: func(next *cooperative.Scope, call func(*cooperative.Scope, string) error) error {
			_, err := state.OnSessionSwitch(next, bootstrapstate.NewListener(call))
			return err
		},
		ProcessRunning: func(pid float64) bool { return syscall.Kill(int(pid), 0) == nil }, Platform: func() string { return runtime.GOOS },
		Debug: func(message string) { debugLogs = append(debugLogs, message) }, ErrorMessage: func(e error) string { return e.Error() },
	}
	service, e := concurrentsessions.New(owner, cleanup, files, host)
	if e != nil {
		owner.Close()
		return e
	}
	sessions := filepath.Join(home, ".claude", "sessions")
	pidPath := filepath.Join(sessions, strconv.Itoa(os.Getpid())+".json")
	trace := []map[string]any{}
	snapshots := []map[string]any{}
	failures := []string{}
	event := func(name string, v map[string]any) {
		if v == nil {
			v = map[string]any{}
		}
		v["event"] = name
		trace = append(trace, v)
	}
	snapshot := func(phase string) error {
		var directory any
		var file any
		info, err := os.Stat(sessions)
		if err == nil {
			dir, err := os.Open(sessions)
			if err != nil {
				return err
			}
			names, err := dir.Readdirnames(-1)
			closed := dir.Close()
			if err != nil {
				return err
			}
			if closed != nil {
				return closed
			}
			directory = map[string]any{"mode": int(info.Mode().Perm()), "isDirectory": info.IsDir(), "entries": names}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		bytes, err := os.ReadFile(pidPath)
		if err == nil {
			info, err := os.Stat(pidPath)
			if err != nil {
				return err
			}
			var parsed any
			if err = json.Unmarshal(bytes, &parsed); err != nil {
				return err
			}
			digest := sha256.Sum256(bytes)
			file = map[string]any{"mode": int(info.Mode().Perm()), "bytes": len(bytes), "sha256": hex.EncodeToString(digest[:]), "rawBase64": base64.StdEncoding.EncodeToString(bytes), "parsed": parsed}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		snapshots = append(snapshots, map[string]any{"phase": phase, "observedAt": time.Now().UnixMilli(), "directory": directory, "file": file})
		event("snapshot", map[string]any{"phase": phase, "exists": file != nil})
		return nil
	}
	before := time.Now().UnixMilli()
	registered := false
	cleanupComplete := false
	postCleanupComplete := false
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		program := func() error {
			event("program:begin", map[string]any{"program": p.Program})
			if e := snapshot("before"); e != nil {
				return e
			}
			event("register:call", nil)
			registration, e := service.RegisterSession(scope)
			if e != nil {
				return e
			}
			event("register:return", map[string]any{"returnedPromise": registration != nil})
			if p.Program == "A" {
				registered, e = registration.Await(scope)
				if e != nil {
					return e
				}
				event("register:await-fulfilled", map[string]any{"registered": registered})
				if e = snapshot("after-registration"); e != nil {
					return e
				}
				event("cleanup:call", nil)
				cleaned, e := cleanup.Start(scope)
				if e != nil {
					return e
				}
				event("cleanup:return", map[string]any{"returnedPromise": cleaned != nil})
				if _, e = cleaned.Await(scope); e != nil {
					return e
				}
				cleanupComplete = true
				event("cleanup:await-fulfilled", nil)
			} else {
				event("cleanup:call", nil)
				cleaned, e := cleanup.Start(scope)
				if e != nil {
					return e
				}
				event("cleanup:return", map[string]any{"returnedPromise": cleaned != nil})
				joined, e := all(scope, owner, registration, cleaned)
				if e != nil {
					return e
				}
				value, e := joined.Await(scope)
				if e != nil {
					return e
				}
				registered = value.Registered
				cleanupComplete = true
				event("parallel:await-all-fulfilled", map[string]any{"registered": registered, "cleanupOutcomeUndefined": true})
			}
			if e = snapshot("after-primary"); e != nil {
				return e
			}
			if p.PostCleanup {
				event("post-cleanup:call", nil)
				cleaned, e := cleanup.Start(scope)
				if e != nil {
					return e
				}
				event("post-cleanup:return", map[string]any{"returnedPromise": cleaned != nil})
				if _, e = cleaned.Await(scope); e != nil {
					return e
				}
				postCleanupComplete = true
				event("post-cleanup:await-fulfilled", nil)
				if e = snapshot("after-post-cleanup"); e != nil {
					return e
				}
			}
			return nil
		}
		failure := program()
		if failure != nil {
			failures = append(failures, failure.Error())
		}
		result := map[string]any{"input": p, "actualPid": os.Getpid(), "home": home, "sessions": sessions, "pidPath": pidPath, "before": before, "after": time.Now().UnixMilli(), "sessionId": state.SessionID(), "originalCwd": state.Snapshot().OriginalCWD, "registered": registered, "cleanupComplete": cleanupComplete, "postCleanupComplete": postCleanupComplete, "snapshots": snapshots, "trace": trace, "errors": failures, "GoOnlyDebugCalls": debugLogs, "bootstrapSource": "actual same-owner bootstrapstate.State and OnSessionSwitch", "fixtureScope": "original three programs; no main/graceful exit or native ref policy"}
		if err := writeJSON(filepath.Join(home, "result.json"), result); err != nil {
			return err
		}
		return failure
	})
	// These receipts happen only after the original program/result capture.
	// They protect disposable test resources and do not represent natural exit.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	admin := map[string]any{"resultWrittenBeforeJoin": true, "joinIsAdministrativeNotSourceNaturalDrain": true, "programError": nil, "firstJoinError": nil, "lastJoinError": nil}
	if e != nil {
		admin["programError"] = e.Error()
	}
	if err := files.Join(ctx); err != nil {
		admin["firstJoinError"] = err.Error()
	}
	owner.Close()
	if err := files.Join(ctx); err != nil {
		admin["lastJoinError"] = err.Error()
	}
	_, err := os.Stat(pidPath)
	admin["physicalPIDFileAfterAdministration"] = err == nil
	admin["ownerClosed"] = true
	if err := writeJSON(filepath.Join(home, "go-administrative-observation.json"), admin); err != nil {
		return err
	}
	return e
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
