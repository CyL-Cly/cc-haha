package resumestate_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistorycopy"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

// This is the original program input, not captured Source output. Source2 and
// control2 returned the full ordered replacement array; the original actual
// remove-plan-await mutant2 returned the old array in the sealed independent
// probe. That existing field, not future readiness, is the regression target.
const originalQueuedReplace = `{"id":"original-array-queued-replace","program":"consumer","primeProject":true,"sdkEnabled":true,"disabled":false,"currentSessionId":"22222222-2222-4222-8222-222222222222","previousSessionId":"11111111-1111-4111-8111-111111111111","log":{"date":"2026-01-01T00:00:00.000Z","messages":[{"type":"user","message":{"role":"user","content":"original resumed user input"},"uuid":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","timestamp":"2026-01-01T00:00:00.000Z","cwd":{"private":"cwd"},"userType":"external","sessionId":"11111111-1111-4111-8111-111111111111","version":"fixture-version"}],"value":0,"created":"2026-01-01T00:00:00.000Z","modified":"2026-01-01T00:00:00.000Z","firstPrompt":"original resumed user input","messageCount":1,"isSidechain":false,"fileHistorySnapshots":[{"messageId":"aaaaaaaa-aaaa-4aaa-8aaa-000000000001","trackedFileBackups":{"edited.txt":{"backupFileName":"backup-one","version":1,"backupTime":"2026-01-01T00:00:00.000Z"},"new.txt":{"backupFileName":null,"version":1,"backupTime":"2026-01-01T00:00:00.000Z"}},"timestamp":"2026-01-01T00:00:00.000Z","schemaVersion":2,"completedFileBackups":{"completed.txt":{"backupFileName":"backup-two","version":1,"backupTime":"2026-01-01T00:00:00.000Z"}}},{"messageId":"aaaaaaaa-aaaa-4aaa-8aaa-000000000002","trackedFileBackups":{"second.txt":{"backupFileName":"backup-three","version":1,"backupTime":"2026-01-01T00:00:00.000Z"}},"timestamp":"2026-01-01T00:00:00.000Z","schemaVersion":2}]},"setup":[{"scope":"old","name":"backup-one","kind":"file","rawBase64":"Zmlyc3QgYmFja3VwAGxpdGVyYWwK","mode":384},{"scope":"old","name":"backup-two","kind":"file","rawBase64":"Y29tcGxldGVkIOS4reaWhwo=","mode":384},{"scope":"old","name":"backup-three","kind":"file","rawBase64":"dGhpcmQgYmFja3VwCg==","mode":416}],"mutation":{"kind":"queued-replace","items":[{"messageId":"aaaaaaaa-aaaa-4aaa-8aaa-000000000001","trackedFileBackups":{"edited.txt":{"backupFileName":"backup-one","version":1,"backupTime":"2026-01-01T00:00:00.000Z"},"new.txt":{"backupFileName":null,"version":1,"backupTime":"2026-01-01T00:00:00.000Z"}},"timestamp":"2026-01-01T00:00:00.000Z","schemaVersion":2,"completedFileBackups":{"completed.txt":{"backupFileName":"backup-two","version":1,"backupTime":"2026-01-01T00:00:00.000Z"}}}]}}`

func quote(s string) json.RawMessage { v, _ := json.Marshal(s); return v }
func field(o protocol.JSONObject, k string) string {
	var s string
	_ = json.Unmarshal(o.Get(k), &s)
	return s
}
func checked(home, path string) error {
	if !filepath.IsAbs(path) || path != filepath.Clean(path) || path != home && !strings.HasPrefix(path, home+string(filepath.Separator)) {
		return errors.New("private filesystem capability refused " + path)
	}
	for p := path; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e == nil && st.Mode()&os.ModeSymlink != 0 {
			return errors.New("private symlink capability refused " + path)
		}
		if e != nil && !errors.Is(e, fs.ErrNotExist) {
			return e
		}
		if p == home {
			break
		}
	}
	return nil
}

type native struct{ home string }

func (n native) MkdirAll(p string) error {
	if e := checked(n.home, p); e != nil {
		return e
	}
	return (filehistorycopy.OSFiles{}).MkdirAll(p)
}
func (n native) Link(a, b string) error {
	if e := checked(n.home, a); e != nil {
		return e
	}
	if e := checked(n.home, b); e != nil {
		return e
	}
	return os.Link(a, b)
}
func (n native) CopyFile(a, b string) error {
	if e := checked(n.home, a); e != nil {
		return e
	}
	if e := checked(n.home, b); e != nil {
		return e
	}
	return (filehistorycopy.OSFiles{}).CopyFile(a, b)
}

type transcript struct{ home string }

func (n transcript) OpenFile(p string, f int, m fs.FileMode) (sessioncontroller.TranscriptFile, error) {
	if e := checked(n.home, p); e != nil {
		return nil, e
	}
	return (sessioncontroller.OSFiles{}).OpenFile(p, f, m)
}
func (n transcript) Stat(p string) (fs.FileInfo, error) {
	if e := checked(n.home, p); e != nil {
		return nil, e
	}
	return os.Stat(p)
}
func (n transcript) MkdirAll(p string, m fs.FileMode) error {
	if e := checked(n.home, p); e != nil {
		return e
	}
	return os.MkdirAll(p, m)
}
func (n transcript) ReadFile(p string) ([]byte, error) {
	if e := checked(n.home, p); e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}

type loaderFS struct{ home string }

func (n loaderFS) Open(p string) (sessionstore.File, error) {
	if e := checked(n.home, p); e != nil {
		return nil, e
	}
	return (sessionstore.OSFileSystem{}).Open(p)
}
func (n loaderFS) ReadFile(p string) ([]byte, error) {
	if e := checked(n.home, p); e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}

// All calls are original immutable native IO, guarded independently of business.
type resumeNative struct{ home string }

func (n resumeNative) Open(p string) (sessionstore.File, error) { return (loaderFS{n.home}).Open(p) }
func (n resumeNative) ReadFile(p string) ([]byte, error)        { return (loaderFS{n.home}).ReadFile(p) }
func (n resumeNative) WriteFile(p string, b []byte) error {
	if e := checked(n.home, p); e != nil {
		return e
	}
	return os.WriteFile(p, b, 0666)
}
func (n resumeNative) MkdirAll(p string) error {
	if e := checked(n.home, p); e != nil {
		return e
	}
	return os.MkdirAll(p, 0777)
}
func (n resumeNative) Link(a, b string) error     { return (native{n.home}).Link(a, b) }
func (n resumeNative) CopyFile(a, b string) error { return (native{n.home}).CopyFile(a, b) }
func (n resumeNative) ReadDir(p string) ([]fs.DirEntry, error) {
	if e := checked(n.home, p); e != nil {
		return nil, e
	}
	return os.ReadDir(p)
}

func actualUUID() string {
	var value [16]byte
	if _, e := rand.Read(value[:]); e != nil {
		panic(e)
	}
	value[6] = value[6]&15 | 64
	value[8] = value[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
}

func TestPlanAwaitQueuedReplacement(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ADMIN_TEST_PROCESS PID=%d HOME=%s", os.Getpid(), home)
	cwd, configDir := filepath.Join(home, "cwd"), filepath.Join(home, ".claude")
	for _, path := range []string{cwd, configDir} {
		if err = os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var input protocol.JSONObject
	if err = json.Unmarshal([]byte(originalQueuedReplace), &input); err != nil {
		t.Fatal(err)
	}
	sid := field(input, "currentSessionId")
	oldSID := field(input, "previousSessionId")
	oldDir := filepath.Join(configDir, "file-history", oldSID)
	if err = os.MkdirAll(filepath.Dir(oldDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(oldDir, 0700); err != nil {
		t.Fatal(err)
	}
	var setup []struct {
		Scope, Name, Kind, RawBase64 string
		Mode                         fs.FileMode
	}
	if err = json.Unmarshal(input.Get("setup"), &setup); err != nil {
		t.Fatal(err)
	}
	for _, item := range setup {
		if item.Scope != "old" || item.Kind != "file" || filepath.Base(item.Name) != item.Name {
			t.Fatal("unexpected original setup")
		}
		raw, e := base64.StdEncoding.DecodeString(item.RawBase64)
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(oldDir, item.Name)
		if e = os.WriteFile(path, raw, item.Mode); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(path, item.Mode); e != nil {
			t.Fatal(e)
		}
	}
	owner := cooperative.New()
	defer owner.Close()
	state, err := bootstrapstate.New(owner, sid, cwd, func() (string, error) { return "", errors.New("undeclared session UUID") })
	if err != nil {
		t.Fatal(err)
	}
	settings, err := clisettings.NewState(func() float64 { return float64(time.Now().UnixMilli()) })
	if err != nil {
		t.Fatal(err)
	}
	settingService, err := clisettings.NewService(clisettings.Options{State: settings, ConfigHome: configDir, OriginalCWD: cwd, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return float64(time.Now().UnixMilli()) }, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &configDir})
	if err != nil {
		t.Fatal(err)
	}
	defer global.Close()
	envMap := map[string]string{"NODE_ENV": "production", "USER_TYPE": "external", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING": "1", "CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING": "0"}
	env, err := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(configDir)}, protocol.JSONField{Name: "USER_TYPE", Value: quote("external")}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
	if err != nil {
		t.Fatal(err)
	}
	startup, err := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settingService, SettingsState: settings, Environment: env, ConfigHome: configDir, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = startup.GetInitialSettings(); err != nil {
		t.Fatal(err)
	}
	var diagnosticMu sync.Mutex
	var failures []error
	onError := func(err error) { diagnosticMu.Lock(); defer diagnosticMu.Unlock(); failures = append(failures, err) }
	writer, err := sessioncontroller.NewWriter(sessioncontroller.WriterOptions{Bootstrap: state, ConfigDir: configDir, CWD: cwd, SessionID: sid, Version: "fixture-version", Environment: envMap, SettingsState: settings, Files: transcript{home}, Branch: func() (string, error) { return "", cooperative.ErrRestricted }, OnError: onError, Schedule: sessioncontroller.RealSchedule, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sessioncontroller.NewPendingWriter(writer, owner)
	if err != nil {
		t.Fatal(err)
	}
	files := loaderFS{home}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: files, Stat: func(p string) (int64, error) {
		if e := checked(home, p); e != nil {
			return 0, e
		}
		st, e := os.Stat(p)
		if e != nil {
			return 0, e
		}
		return st.Size(), nil
	}, ReadFile: files.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if err != nil {
		t.Fatal(err)
	}
	metadataPaths := map[string]bool{}
	for p := home; ; p = filepath.Dir(p) {
		path := filepath.Join(p, ".git")
		if _, e := os.Lstat(path); !errors.Is(e, fs.ErrNotExist) {
			t.Fatalf("administrative refusal: real ancestor metadata exists or cannot be checked: %s %v", path, e)
		}
		metadataPaths[path] = true
		if p == filepath.Dir(p) {
			break
		}
	}
	stat := func(p string) (fs.FileInfo, error) {
		if !metadataPaths[p] {
			if e := checked(home, p); e != nil {
				return nil, e
			}
		}
		return os.Stat(p)
	}
	read := func(p string) ([]byte, error) {
		if e := checked(home, p); e != nil {
			return nil, e
		}
		return os.ReadFile(p)
	}
	watch := &gitbranch.NativePollWatch{Stat: stat}
	var registeredCleanup []func(*cooperative.Scope) error
	git, err := gitbranch.New(gitbranch.Options{Owner: owner, CWD: func() string { return state.Snapshot().CWD }, Now: func() int64 { return time.Now().UnixMilli() }, StatSync: stat, Stat: stat, ReadFile: read, WatchFile: watch.Watch, ScrollDraining: func() bool { return false }, RegisterCleanup: func(callback func(*cooperative.Scope) error) { registeredCleanup = append(registeredCleanup, callback) }, NodeEnv: "production"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = git.Close(context.Background()); watch.Join() }()
	sink, err := sessioncontroller.NewFileHistorySink(pending, loader)
	if err != nil {
		t.Fatal(err)
	}
	copyService, err := filehistorycopy.New(filehistorycopy.Options{Owner: owner, Binding: pending.NativeCopyBinding(), Sink: sink, Files: native{home}, Environment: func(k string) string { return envMap[k] }, NonInteractive: func() bool { return true }, ConfigHome: func() string { return configDir }, SessionID: state.SessionID, OnError: onError, Debug: func(string) {}, Event: func(string, protocol.JSONObject) {}})
	if err != nil {
		t.Fatal(err)
	}
	factory, err := messages.NewFactory(actualUUID, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reducer, err := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(raw json.RawMessage) (json.RawMessage, error) { return sessionresume.RelativePath(cwd, home, raw) }, OnDiagnostic: func(sessionresume.Diagnostic) {}, OnError: onError})
	if err != nil {
		t.Fatal(err)
	}
	resume, err := resumestate.NewOwned(resumestate.Options{ConfigDir: configDir, CWD: cwd, OriginalCWD: cwd, CurrentSessionID: sid, NonInteractive: true, Environment: envMap, Files: resumeNative{home}, Stat: func(p string) (resumestate.Stat, error) {
		if e := checked(home, p); e != nil {
			return resumestate.Stat{}, e
		}
		st, e := os.Stat(p)
		if e != nil {
			return resumestate.Stat{}, e
		}
		return resumestate.Stat{Size: st.Size(), Modified: float64(st.ModTime().UnixMilli())}, nil
	}, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Now: func() int64 { return time.Now().UnixMilli() }, UUID: actualUUID, OnDiagnostic: func(sessionresume.Diagnostic) {}, OnError: onError, OnPhase: func(string) {}, PolicyPaths: []string{filepath.Join(home, "policy/managed-settings.json")}, SettingsState: settings}, state, startup)
	if err != nil {
		t.Fatal(err)
	}
	if err = resume.BindOwnedFileHistory(copyService); err != nil {
		t.Fatal(err)
	}
	if err = resume.BindOwnedNative(pending.NativeCopyBinding(), func(scope *cooperative.Scope, sid string, value *sessionhistory.Transcript) error {
		_, _, e := pending.PrimeSessionUUIDCache(scope, loader, sid, value)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	var rawLog, mutation protocol.JSONObject
	if err = json.Unmarshal(input.Get("log"), &rawLog); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(input.Get("mutation"), &mutation); err != nil {
		t.Fatal(err)
	}
	var rows []json.RawMessage
	if err = json.Unmarshal(rawLog.Get("messages"), &rows); err != nil {
		t.Fatal(err)
	}
	for i, raw := range rows {
		var m protocol.JSONObject
		if err = json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		m.Set("cwd", quote(cwd))
		rows[i], err = m.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
	}
	rawRows, _ := json.Marshal(rows)
	rawLog.Set("messages", rawRows)
	original, err := resumestate.DecodeOriginalLog(rawLog)
	if err != nil {
		t.Fatal(err)
	}
	replacementFields := rawLog.Clone()
	replacementFields.Set("fileHistorySnapshots", mutation.Get("items"))
	replacement, err := filehistorycopy.DecodeLog(replacementFields)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(replacement.Snapshots.Items)
	if err != nil {
		t.Fatal(err)
	}
	if len(replacement.Snapshots.Items) != 1 || len(original.History.Snapshots.Items) != 2 {
		t.Fatal("exact original array recipe required")
	}
	var returnedRaw []byte
	// The initial prime Record+Flush are original input operations. They precede
	// BeginLoad and are not a copy/result completion barrier.
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: json.RawMessage(`{"role":"user","content":"original explicit prime Project program input"}`)}, protocol.JSONField{Name: "uuid", Value: quote("cccccccc-cccc-4ccc-8ccc-cccccccccccc")}, protocol.JSONField{Name: "timestamp", Value: quote("2026-01-01T00:00:00.000Z")})
		token, e := state.Context(scope)
		if e != nil {
			return e
		}
		prime, e := pending.BeginRecordLive(scope, loader, sessioncontroller.LiveRecordProfile{QuerySource: "sdk"}, []sessioncontroller.LiveRecord{{Record: &user}}, sessioncontroller.RecordOptions{PromptID: func() json.RawMessage { return quote("original-file-history-program-prompt") }, CWDContext: token, Branch: func(next *cooperative.Scope) (sessioncontroller.RecordBranchFuture, error) {
			return git.BranchWithCWD(next, func(next *cooperative.Scope) (string, error) { return token.GetCWD(next) })
		}})
		if e != nil {
			return e
		}
		if _, e = prime.Await(scope); e != nil {
			return e
		}
		flush, e := pending.Flush(scope)
		if e != nil {
			return e
		}
		if e = flush.Await(scope); e != nil {
			return e
		}
		future, e := resume.BeginLoad(scope, resumestate.Input{Original: original})
		if e != nil {
			return e
		}
		// One original queueMicrotask, immediately after public call: no await,
		// held-read barrier, collector or readiness inspection precedes it.
		_, e = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { original.ReplaceSnapshots(replacement.Snapshots); return nil })
		if e != nil {
			return e
		}
		result, e := future.Await(scope)
		if e != nil {
			return e
		}
		if result == nil {
			return errors.New("public BeginLoad unexpectedly returned null")
		}
		got := result.MetadataSnapshot().Get("fileHistorySnapshots")
		t.Logf("EXPECTED_RETURNED_SNAPSHOTS=%s", expected)
		t.Logf("ACTUAL_RETURNED_SNAPSHOTS=%s", got)
		if !bytes.Equal(got, expected) {
			t.Errorf("public returned fileHistorySnapshots retained old array: got %s want %s", got, expected)
		}
		returnedRaw, e = result.MarshalJSON()
		if e != nil {
			return e
		}
		var actualReturn protocol.JSONObject
		if e = json.Unmarshal(returnedRaw, &actualReturn); e != nil {
			return e
		}
		if !bytes.Equal(actualReturn.Get("fileHistorySnapshots"), expected) {
			t.Errorf("actual public return serialization retained old array: got %s want %s", actualReturn.Get("fileHistorySnapshots"), expected)
		}
		t.Logf("COMPLETE_ORDERED_RETURNED_BASE64=%s", base64.StdEncoding.EncodeToString(returnedRaw))
		return nil
	})
	// Administrative fixture cleanup occurs ONLY after the actual returned field
	// was asserted. It does not join copy/Record to make the business result green.
	drainErr := pending.WaitRuntimeNative(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if drainErr != nil {
		t.Fatal(drainErr)
	}
	diagnosticMu.Lock()
	defer diagnosticMu.Unlock()
	if len(failures) != 0 {
		t.Fatalf("unexpected actual diagnostics: %v", failures)
	}
	if len(registeredCleanup) != 0 {
		t.Fatal("actual no-repo should not register a Git watcher")
	}
}
