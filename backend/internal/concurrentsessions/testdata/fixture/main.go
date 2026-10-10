// This executable is a finite original-input component fixture, not a CLI
// bootstrap/signal implementation. Administrative native observation starts
// strictly after its original program/result snapshots have been written.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/concurrentsessions"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

type profile struct {
	ID            string   `json:"id"`
	Program       string   `json:"program"`
	SessionID     string   `json:"sessionId"`
	NextSessionID string   `json:"nextSessionId"`
	Features      []string `json:"features"`
	Env           struct {
		Kind       string `json:"kind"`
		Name       string `json:"name"`
		LogName    string `json:"logName"`
		Agent      string `json:"agent"`
		SocketName string `json:"socketName"`
	} `json:"env"`
	Setup string `json:"setup"`
	Umask int    `json:"umask"`
}
type peers struct {
	LivePID         *int `json:"livePid"`
	StalePID        *int `json:"stalePid"`
	ParentAllocated bool `json:"parentAllocated"`
}
type receipt struct {
	Operation, Path string
	SameOwner       bool
	Done            <-chan struct{}
	Wait            func(context.Context) error
}

// The forwarding observer returns the exact original Future object. It neither
// registers Then nor wraps business completion; receipts are read only after
// the original program snapshots and administrative Owner.Close.
type observedFiles struct {
	native   *concurrentsessions.NativeFileSystem
	owner    *cooperative.Owner
	mu       sync.Mutex
	receipts []receipt
}

func remember[T any](files *observedFiles, scope *cooperative.Scope, operation, path string, future *ownerasync.OwnerFuture[T], err error) (*ownerasync.OwnerFuture[T], error) {
	if future != nil {
		files.mu.Lock()
		files.receipts = append(files.receipts, receipt{operation, path, scope.BelongsTo(files.owner), future.Done(), func(ctx context.Context) error { _, e := future.Wait(ctx); return e }})
		files.mu.Unlock()
	}
	return future, err
}
func (f *observedFiles) Mkdir(s *cooperative.Scope, p string, m os.FileMode) (*concurrentsessions.VoidFuture, error) {
	v, e := f.native.Mkdir(s, p, m)
	return remember(f, s, "mkdir", p, v, e)
}
func (f *observedFiles) Chmod(s *cooperative.Scope, p string, m os.FileMode) (*concurrentsessions.VoidFuture, error) {
	v, e := f.native.Chmod(s, p, m)
	return remember(f, s, "chmod", p, v, e)
}
func (f *observedFiles) WriteFile(s *cooperative.Scope, p string, b []byte, m os.FileMode) (*concurrentsessions.VoidFuture, error) {
	v, e := f.native.WriteFile(s, p, b, m)
	return remember(f, s, "writeFile", p, v, e)
}
func (f *observedFiles) ReadFile(s *cooperative.Scope, p string) (*ownerasync.OwnerFuture[[]byte], error) {
	v, e := f.native.ReadFile(s, p)
	return remember(f, s, "readFile", p, v, e)
}
func (f *observedFiles) ReadDir(s *cooperative.Scope, p string) (*ownerasync.OwnerFuture[[]string], error) {
	v, e := f.native.ReadDir(s, p)
	return remember(f, s, "readdir", p, v, e)
}
func (f *observedFiles) Unlink(s *cooperative.Scope, p string) (*concurrentsessions.VoidFuture, error) {
	v, e := f.native.Unlink(s, p)
	return remember(f, s, "unlink", p, v, e)
}
func raw(value any) json.RawMessage {
	b, e := json.Marshal(value)
	if e != nil {
		panic(e)
	}
	return b
}
func errorView(err error) map[string]any {
	return map[string]any{"name": "GoError", "message": err.Error(), "type": fmt.Sprintf("%T", err), "sourceNativeDiagnosticUnimplemented": true}
}
func writeJSON(p string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return os.WriteFile(p, b, 0666)
}
func decodeFile(p string, v any) error {
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	var more any
	if e = d.Decode(&more); e != io.EOF {
		return errors.New("exact one original JSON input required")
	}
	return nil
}
func allowed(v string, values ...string) bool {
	for _, c := range values {
		if v == c {
			return true
		}
	}
	return false
}
func contains(values []string, v string) bool {
	for _, s := range values {
		if s == v {
			return true
		}
	}
	return false
}
func validProfile(p profile) bool {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if p.ID == "" || !uuid.MatchString(p.SessionID) || !uuid.MatchString(p.NextSessionID) || p.Umask != 18 || len(p.Features) == 0 || p.Features[0] != "TRANSCRIPT_CLASSIFIER" || !allowed(p.Env.Kind, "bg", "daemon", "daemon-worker", "invalid") || !regexp.MustCompile(`^[a-z-]+\.log$`).MatchString(p.Env.LogName) || !regexp.MustCompile(`^[a-z-]+\.sock$`).MatchString(p.Env.SocketName) {
		return false
	}
	if !allowed(p.Program, "registration-fields", "updates", "sid-switch", "sid-switch-count", "best-effort", "count-owned-peers", "count-owned-peers-leadingzero", "count-missing", "count-nondirectory", "teammate-skip") || !allowed(p.Setup, "empty", "missing-own-file", "malformed-own-file", "null-own-file", "string-own-file", "array-own-file", "number-own-file", "futurefields-own-file", "own-file-directory", "sessions-directory-file") {
		return false
	}
	seen := map[string]bool{}
	for _, v := range p.Features {
		if seen[v] || !allowed(v, "TRANSCRIPT_CLASSIFIER", "BG_SESSIONS", "UDS_INBOX") {
			return false
		}
		seen[v] = true
	}
	return true
}
func privateHome() (string, error) {
	h := os.Getenv("HOME")
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if !filepath.IsAbs(h) || !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(h), "cc-haha-concurrent-native-") {
		return "", errors.New("explicit disposable HOME required")
	}
	hc, e := filepath.EvalSymlinks(h)
	if e != nil || hc != h {
		return "", errors.New("canonical private HOME required")
	}
	rc, e := filepath.EvalSymlinks(root)
	if e != nil || filepath.Dir(h) != rc {
		return "", errors.New("private HOME must be exact parent-owned temporary leaf")
	}
	if os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(h, ".claude") {
		return "", errors.New("exact original private configuration required")
	}
	for _, p := range []string{h, filepath.Join(h, ".claude"), filepath.Join(h, "input.json"), filepath.Join(h, "peers.json")} {
		s, e := os.Lstat(p)
		if e != nil && errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || s.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("private ancestors/inputs must not be symlinks")
		}
	}
	return h, nil
}
func processRunning(pid float64) bool {
	// Source process.kill accepts finite integral PID values within signed int32;
	// invalid/overflow values are exceptions caught by isProcessRunning, not a
	// lossy float->int probe of a different process.
	if math.IsNaN(pid) || math.IsInf(pid, 0) || pid <= 1 || pid > math.MaxInt32 || math.Trunc(pid) != pid {
		return false
	}
	return syscall.Kill(int(pid), 0) == nil
}
func names(path string) ([]string, error) {
	d, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer d.Close()
	return d.Readdirnames(-1)
}
func rawNode(p string) any {
	info, e := os.Lstat(p)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return map[string]any{"type": "observer-error", "error": errorView(e)}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return map[string]any{"type": "observer-error", "error": map[string]any{"message": "native Darwin stat receipt unavailable"}}
	}
	view := map[string]any{"mode": int(info.Mode().Perm()), "mtimeNs": strconv.FormatInt(stat.Mtimespec.Sec*1e9+stat.Mtimespec.Nsec, 10), "ctimeNs": strconv.FormatInt(stat.Ctimespec.Sec*1e9+stat.Ctimespec.Nsec, 10), "ino": strconv.FormatUint(stat.Ino, 10), "dev": strconv.FormatInt(int64(stat.Dev), 10)}
	if info.IsDir() {
		v, e := names(p)
		if e != nil {
			return map[string]any{"type": "observer-error", "error": errorView(e)}
		}
		view["type"] = "directory"
		view["entries"] = v
		return view
	}
	if !info.Mode().IsRegular() {
		view["type"] = "other"
		return view
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return map[string]any{"type": "observer-error", "error": errorView(e)}
	}
	sum := sha256.Sum256(b)
	view["type"] = "file"
	view["bytes"] = len(b)
	view["sha256"] = hex.EncodeToString(sum[:])
	view["rawBase64"] = base64.StdEncoding.EncodeToString(b)
	decoded := protocol.DecodeJavaScriptUTF8(b)
	if message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(string(decoded)); invalid {
		view["parseError"] = map[string]any{"name": "SyntaxError", "message": json.RawMessage(message), "stackUnimplemented": true}
	} else if value, e := protocol.ParseAndStringifyJavaScriptJSON(decoded); e == nil {
		view["parsed"] = json.RawMessage(value)
	} else {
		view["parseError"] = errorView(e)
	}
	return view
}
func strptr(v string) *string { return &v }
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if runtime.GOOS != "darwin" {
		return errors.New("this actual stat fixture is limited to the current Darwin native target")
	}
	h, e := privateHome()
	if e != nil {
		return e
	}
	if len(os.Args) == 2 && os.Args[1] == "--original-peer" {
		if e := writeJSON(filepath.Join(h, "ready.json"), map[string]any{"actualPid": os.Getpid(), "home": h}); e != nil {
			return e
		}
		v, e := io.ReadAll(os.Stdin)
		if e != nil {
			return e
		}
		if string(v) != "finish-original-peer\n" {
			return errors.New("explicit original peer lifetime required")
		}
		return nil
	}
	if len(os.Args) != 1 {
		return errors.New("unsupported fixture args")
	}
	var p profile
	if e = decodeFile(filepath.Join(h, "input.json"), &p); e != nil {
		return e
	}
	if !validProfile(p) {
		return errors.New("finite original session contract input required")
	}
	var peers peers
	if e = decodeFile(filepath.Join(h, "peers.json"), &peers); e != nil {
		return e
	}
	wantPeers := strings.HasPrefix(p.Program, "count-owned-peers")
	if !peers.ParentAllocated || wantPeers != (peers.LivePID != nil && peers.StalePID != nil) {
		return errors.New("owned real peer provenance required")
	}
	for _, v := range []*int{peers.LivePID, peers.StalePID} {
		if v != nil && (*v <= 1 || *v == os.Getpid()) {
			return errors.New("invalid owned peer PID")
		}
	}
	if wantPeers && (!processRunning(float64(*peers.LivePID)) || processRunning(float64(*peers.StalePID))) {
		return errors.New("real live and actually reaped stale peers required")
	}
	expectedEnv := map[string]string{"CLAUDE_CODE_ENTRYPOINT": "native-session-contract", "CLAUDE_CODE_SESSION_KIND": p.Env.Kind, "CLAUDE_CODE_SESSION_NAME": p.Env.Name, "CLAUDE_CODE_SESSION_LOG": filepath.Join(h, p.Env.LogName), "CLAUDE_CODE_AGENT": p.Env.Agent, "CLAUDE_CODE_MESSAGING_SOCKET": filepath.Join(h, p.Env.SocketName)}
	for k, v := range expectedEnv {
		if os.Getenv(k) != v {
			return errors.New("private original environment derivation required")
		}
	}
	syscall.Umask(p.Umask)
	config := filepath.Join(h, ".claude")
	sessions := filepath.Join(config, "sessions")
	own := filepath.Join(sessions, strconv.Itoa(os.Getpid())+".json")
	sid := p.SessionID
	cwd, e := os.Getwd()
	if e != nil {
		return e
	}
	var agent *string
	if p.Program == "teammate-skip" {
		agent = strptr("original-private-teammate")
	}
	owner := cooperative.New()
	cleanup, e := runtimecleanup.New(owner)
	if e != nil {
		owner.Close()
		return e
	}
	native, e := concurrentsessions.NewNativeFileSystem(owner)
	if e != nil {
		owner.Close()
		return e
	}
	files := &observedFiles{native: native, owner: owner}
	callbacks := []func(*cooperative.Scope, string) error{}
	service, e := concurrentsessions.New(owner, cleanup, files, concurrentsessions.Host{PID: os.Getpid(), BG: contains(p.Features, "BG_SESSIONS"), UDS: contains(p.Features, "UDS_INBOX"), ConfigHome: func() string { return config }, SessionID: func() string { return sid }, OriginalCWD: func() string { return cwd }, NowMilliseconds: func() int64 { return time.Now().UnixMilli() }, Environment: os.LookupEnv, AgentID: func() *string { return agent }, SubscribeSessionSwitch: func(_ *cooperative.Scope, cb func(*cooperative.Scope, string) error) error {
		callbacks = append(callbacks, cb)
		return nil
	}, ProcessRunning: processRunning, Platform: func() string { return "macos" }, Debug: func(string) { /* independently declared debug-inactive mechanical external host */ }, ErrorMessage: func(err error) string { return err.Error() }})
	if e != nil {
		owner.Close()
		return e
	}
	snapshots := []any{}
	trace := []any{}
	failures := []any{}
	event := func(name string, v map[string]any) {
		if v == nil {
			v = map[string]any{}
		}
		v["event"] = name
		trace = append(trace, v)
	}
	snapshot := func(phase string) {
		dir := rawNode(sessions)
		entries := map[string]any{}
		ns := []string{}
		if d, ok := dir.(map[string]any); ok && d["type"] == "directory" {
			ns = d["entries"].([]string)
			for _, n := range ns {
				entries[n] = rawNode(filepath.Join(sessions, n))
			}
		}
		ownNode := rawNode(own)
		typ := "absent"
		if d, ok := ownNode.(map[string]any); ok {
			typ, _ = d["type"].(string)
		}
		snapshots = append(snapshots, map[string]any{"phase": phase, "observedAt": time.Now().UnixMilli(), "own": ownNode, "sessions": dir, "entries": entries})
		event("snapshot", map[string]any{"phase": phase, "ownType": typ, "names": ns})
	}
	originalRaw := map[string]string{"malformed-own-file": "{\"incomplete\":", "null-own-file": "null", "string-own-file": "\"中文🚀\"", "array-own-file": "[\"old\",null,{\"future\":1}]", "number-own-file": "9007199254740993", "futurefields-own-file": "{\"unknown\":{\"large\":9007199254740993,\"nil\":null},\"name\":\"original\",\"bridgeSessionId\":\"old\"}"}
	before := time.Now().UnixMilli()
	var registered *bool
	var count *int
	program := func(scope *cooperative.Scope) error {
		awaitVoid := func(v *concurrentsessions.VoidFuture, e error) error {
			if e != nil {
				return e
			}
			_, e = v.Await(scope)
			return e
		}
		countCall := func() error {
			v, e := service.CountConcurrentSessions(scope)
			if e != nil {
				return e
			}
			x, e := v.Await(scope)
			if e == nil {
				count = &x
			}
			return e
		}
		snapshot("before")
		if p.Setup == "sessions-directory-file" {
			if e := os.WriteFile(sessions, []byte("ORIGINAL-NON-DIRECTORY-SESSIONS\n"), 0666); e != nil {
				return e
			}
		}
		if p.Program == "registration-fields" && p.Setup == "own-file-directory" {
			if e := os.Mkdir(sessions, 0700); e != nil {
				return e
			}
			if e := os.Mkdir(own, 0700); e != nil {
				return e
			}
		}
		if p.Program != "count-missing" && p.Program != "count-nondirectory" {
			event("register:call", nil)
			v, e := service.RegisterSession(scope)
			event("register:return", map[string]any{"returnedPromise": v != nil})
			if e != nil {
				return e
			}
			x, e := v.Await(scope)
			if e != nil {
				return e
			}
			registered = &x
			b, e := service.IsBGSession(scope)
			if e != nil {
				return e
			}
			event("register:await-fulfilled", map[string]any{"registered": x, "isBgSession": b})
			snapshot("after-registration")
		}
		switch p.Program {
		case "updates":
			event("name-undefined:call", nil)
			if e := awaitVoid(service.UpdateSessionName(scope, nil)); e != nil {
				return e
			}
			event("name-undefined:await-fulfilled", nil)
			snapshot("after-undefined-name")
			event("name-empty:call", nil)
			if e := awaitVoid(service.UpdateSessionName(scope, strptr(""))); e != nil {
				return e
			}
			event("name-empty:await-fulfilled", nil)
			snapshot("after-empty-name")
			if e := awaitVoid(service.UpdateSessionName(scope, strptr("updated original 中文 name"))); e != nil {
				return e
			}
			event("name-normal:await-fulfilled", nil)
			snapshot("after-name")
			if e := awaitVoid(service.UpdateSessionBridgeID(scope, strptr("bridge-original"))); e != nil {
				return e
			}
			event("bridge-string:await-fulfilled", nil)
			snapshot("after-bridge-string")
			if e := awaitVoid(service.UpdateSessionBridgeID(scope, nil)); e != nil {
				return e
			}
			event("bridge-null:await-fulfilled", nil)
			snapshot("after-bridge-null")
			patch := protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: raw("busy")}, protocol.JSONField{Name: "waitingFor", Value: raw("original-wait-control")})
			if e := awaitVoid(service.UpdateSessionActivity(scope, patch)); e != nil {
				return e
			}
			event("activity-busy:await-fulfilled", nil)
			snapshot("after-activity-busy")
			patch = protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: raw("waiting")})
			if e := awaitVoid(service.UpdateSessionActivity(scope, patch)); e != nil {
				return e
			}
			event("activity-waiting:await-fulfilled", nil)
			snapshot("after-activity-waiting")
		case "sid-switch", "sid-switch-count":
			event("switch:call", nil)
			sid = p.NextSessionID
			for _, cb := range callbacks {
				if e := cb(scope, sid); e != nil {
					return e
				}
			}
			event("switch:return", map[string]any{"returnedUndefined": true, "actualStateSID": sid})
			snapshot("immediately-after-switch")
			if p.Program == "sid-switch" {
				if e := awaitVoid(service.UpdateSessionName(scope, strptr("after-switch-original"))); e != nil {
					return e
				}
				event("public-name:await-fulfilled", nil)
				snapshot("after-public-name")
			} else {
				if e := countCall(); e != nil {
					return e
				}
				event("public-count:await-fulfilled", map[string]any{"count": *count})
				snapshot("after-public-count")
			}
		case "best-effort":
			if p.Setup == "missing-own-file" {
				if e := os.Remove(own); e != nil {
					return e
				}
			} else if p.Setup == "own-file-directory" {
				if e := os.Remove(own); e != nil {
					return e
				}
				if e := os.Mkdir(own, 0700); e != nil {
					return e
				}
			} else {
				v, ok := originalRaw[p.Setup]
				if !ok {
					return errors.New("unsupported original raw setup")
				}
				if e := os.WriteFile(own, []byte(v), 0666); e != nil {
					return e
				}
			}
			snapshot("after-original-raw-setup")
			if e := awaitVoid(service.UpdateSessionName(scope, strptr("best-effort-original"))); e != nil {
				return e
			}
			event("best-effort-name:await-fulfilled", nil)
			snapshot("after-best-effort-name")
			if e := awaitVoid(service.UpdateSessionBridgeID(scope, strptr("best-effort-bridge"))); e != nil {
				return e
			}
			event("best-effort-bridge:await-fulfilled", nil)
			snapshot("after-best-effort-bridge")
		case "count-owned-peers", "count-owned-peers-leadingzero":
			for _, n := range []string{strconv.Itoa(*peers.LivePID) + ".json", strconv.Itoa(*peers.StalePID) + ".json", "0.json"} {
				if e := os.WriteFile(filepath.Join(sessions, n), []byte("ORIGINAL-COUNT-NAMES-ONLY\n"), 0666); e != nil {
					return e
				}
			}
			for _, n := range []string{"2026-03-14_notes.md", strconv.Itoa(os.Getpid()) + ".json.bak", "not-a-session"} {
				if e := os.WriteFile(filepath.Join(sessions, n), []byte("ORIGINAL-PROTECTED-FILENAME\n"), 0666); e != nil {
					return e
				}
			}
			if p.Program == "count-owned-peers-leadingzero" {
				if e := os.WriteFile(filepath.Join(sessions, "00"+strconv.Itoa(*peers.LivePID)+".json"), []byte("ORIGINAL-LIVE-ALIAS\n"), 0666); e != nil {
					return e
				}
			}
			snapshot("before-count")
			if e := countCall(); e != nil {
				return e
			}
			event("count:await-fulfilled", map[string]any{"count": *count})
			snapshot("after-count-return")
		case "count-missing", "count-nondirectory":
			if e := countCall(); e != nil {
				return e
			}
			event("count:await-fulfilled", map[string]any{"count": *count})
			snapshot("after-count-return")
		}
		snapshot("program-end-before-exit")
		return nil
	}
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		failure := program(scope)
		if failure != nil {
			failures = append(failures, map[string]any{"event": "program:throw", "error": errorView(failure)})
		}
		bg, bgErr := service.IsBGSession(scope)
		if bgErr != nil {
			return bgErr
		}
		result := map[string]any{"input": p, "peers": peers, "home": h, "sessions": sessions, "ownPath": own, "actualPid": os.Getpid(), "before": before, "after": time.Now().UnixMilli(), "stateSID": sid, "cwd": cwd, "isBgSession": bg, "snapshots": snapshots, "trace": trace, "errors": failures, "errorLogs": "debug inactive; Source catches do not expose native business error/errno to this caller", "fixtureHost": "finite original mechanical bootstrap adapter; no production CLI switch bus"}
		if registered != nil {
			result["registered"] = *registered
		}
		if count != nil {
			result["count"] = *count
		}
		if writeErr := writeJSON(filepath.Join(h, "result.json"), result); writeErr != nil {
			return writeErr
		}
		return failure
	})
	// These are administrative fixture receipts AFTER original result capture.
	// Neither Join runs before a program snapshot; no cleanup.Registry.Start is
	// called. Source natural event-loop drain/forceExit remains a separate gate.
	admin := map[string]any{"programError": nil, "programResultWrittenBeforeJoin": true, "cleanupRegistryRun": false, "joinIsAdministrativeNotSourceNaturalDrain": true}
	if e != nil {
		admin["programError"] = errorView(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	admin["firstJoinError"] = nil
	if joined := native.Join(ctx); joined != nil {
		admin["firstJoinError"] = errorView(joined)
	}
	admin["diskAfterFirstAdministrativeJoin"] = rawNode(own)
	owner.Close()
	admin["ownerClosed"] = true
	admin["lastJoinError"] = nil
	if joined := native.Join(ctx); joined != nil {
		admin["lastJoinError"] = errorView(joined)
	}
	files.mu.Lock()
	receipts := append([]receipt(nil), files.receipts...)
	files.mu.Unlock()
	settlements := []any{}
	for _, r := range receipts {
		row := map[string]any{"operation": r.Operation, "path": r.Path, "calledOnSameOwner": r.SameOwner, "originalFutureForwarded": true, "doneAfterAdministrativeClose": false, "error": nil}
		select {
		case <-r.Done:
			row["doneAfterAdministrativeClose"] = true
		default:
		}
		if failure := r.Wait(ctx); failure != nil {
			row["error"] = errorView(failure)
		}
		select {
		case <-r.Done:
			row["settledAfterObservation"] = true
		default:
			row["settledAfterObservation"] = false
		}
		settlements = append(settlements, row)
	}
	admin["nativeFutureReceipts"] = settlements
	admin["diskAfterOwnerCloseAndLastJoin"] = rawNode(own)
	if failure := writeJSON(filepath.Join(h, "go-administrative-observation.json"), admin); failure != nil {
		return failure
	}
	return e
}
