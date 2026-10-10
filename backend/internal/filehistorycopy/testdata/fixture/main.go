package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
func physical(home string) (protocol.JSONObject, error) {
	files := protocol.NewJSONObject()
	e := filepath.WalkDir(filepath.Join(home, ".claude"), func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("observer symlink")
		}
		v, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		st, e := os.Stat(p)
		if e != nil {
			return e
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "path", Value: quote(p)}, protocol.JSONField{Name: "rawBase64", Value: quote(base64.StdEncoding.EncodeToString(v))}, protocol.JSONField{Name: "mode", Value: json.RawMessage(fmt.Sprint(uint32(st.Mode().Perm())))}, protocol.JSONField{Name: "size", Value: json.RawMessage(fmt.Sprint(st.Size()))})
		b, e := row.MarshalJSON()
		if e == nil {
			files.Set(filepath.ToSlash(strings.TrimPrefix(p, home+string(filepath.Separator))), b)
		}
		return e
	})
	return files, e
}
func run() error {
	home := os.Getenv("HOME")
	canonical, e := filepath.EvalSymlinks(home)
	if e != nil || canonical != home || !strings.HasPrefix(home, "/private/var/folders/") && !strings.HasPrefix(home, "/tmp/") {
		return errors.New("actual disposable canonical HOME required")
	}
	if os.Getenv("CC_HAHA_RESUME_GO_FIXTURE") != "1" || os.Getenv("NODE_ENV") != "production" || os.Getenv("TZ") != "UTC" {
		return errors.New("private original program environment required")
	}
	inputPath := filepath.Join(home, "input.json")
	if e := checked(home, inputPath); e != nil {
		return e
	}
	b, e := os.ReadFile(inputPath)
	if e != nil {
		return e
	}
	var original protocol.JSONObject
	if e = json.Unmarshal(b, &original); e != nil {
		return e
	}
	if field(original, "program") != "helper" && field(original, "program") != "consumer" {
		return errors.New("original helper/public program required")
	}
	var prime, enabled, disabled bool
	_ = json.Unmarshal(original.Get("primeProject"), &prime)
	_ = json.Unmarshal(original.Get("sdkEnabled"), &enabled)
	_ = json.Unmarshal(original.Get("disabled"), &disabled)
	var rawLog protocol.JSONObject
	if e = json.Unmarshal(original.Get("log"), &rawLog); e != nil {
		return e
	}
	rows := []json.RawMessage{}
	if e = json.Unmarshal(rawLog.Get("messages"), &rows); e != nil {
		return e
	}
	for i, row := range rows {
		var m protocol.JSONObject
		if e = json.Unmarshal(row, &m); e != nil {
			return e
		}
		m.Set("cwd", quote(filepath.Join(home, "cwd")))
		rows[i], e = m.MarshalJSON()
		if e != nil {
			return e
		}
	}
	resolvedRows, _ := json.Marshal(rows)
	rawLog.Set("messages", resolvedRows)
	log, e := filehistorycopy.DecodeLog(rawLog)
	if e != nil {
		return e
	}
	owner := cooperative.New()
	defer owner.Close()
	sid := field(original, "currentSessionId")
	cwd := filepath.Join(home, "cwd")
	configDir := filepath.Join(home, ".claude")
	// Reject an escaping configuration symlink before any shared settings or
	// global-config service can read it. This is fixture safety, not business.
	if e := checked(home, configDir); e != nil {
		return e
	}
	state, e := bootstrapstate.New(owner, sid, cwd, func() (string, error) { return "", errors.New("undeclared runtime UUID capability") })
	if e != nil {
		return e
	}
	settings, e := clisettings.NewState(func() float64 { return float64(time.Now().UnixMilli()) })
	if e != nil {
		return e
	}
	settingService, e := clisettings.NewService(clisettings.Options{State: settings, ConfigHome: configDir, OriginalCWD: cwd, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return float64(time.Now().UnixMilli()) }, PID: os.Getpid()})
	if e != nil {
		return e
	}
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &configDir})
	if e != nil {
		return e
	}
	defer global.Close()
	envMap := map[string]string{"NODE_ENV": "production", "USER_TYPE": "external", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING": "0", "CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING": "0"}
	if enabled {
		envMap["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
	}
	if disabled {
		envMap["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"] = "1"
	}
	environment, e := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(configDir)}, protocol.JSONField{Name: "USER_TYPE", Value: quote("external")}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
	if e != nil {
		return e
	}
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settingService, SettingsState: settings, Environment: environment, ConfigHome: configDir, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if e != nil {
		return e
	}
	if _, e = startup.GetInitialSettings(); e != nil {
		return e
	}
	errs := []string{}
	debug := []string{}
	events := []protocol.JSONObject{}
	onError := func(e error) { errs = append(errs, e.Error()) }
	writer, e := sessioncontroller.NewWriter(sessioncontroller.WriterOptions{Bootstrap: state, ConfigDir: configDir, CWD: cwd, SessionID: sid, Version: "fixture-version", Environment: envMap, SettingsState: settings, Files: transcript{home}, Branch: func() (string, error) { return "", cooperative.ErrRestricted }, OnError: onError, Schedule: sessioncontroller.RealSchedule, Now: time.Now})
	if e != nil {
		return e
	}
	pending, e := sessioncontroller.NewPendingWriter(writer, owner)
	if e != nil {
		return e
	}
	lf := loaderFS{home}
	loader, e := sessionhistory.New(sessionhistory.Options{Files: lf, Stat: func(p string) (int64, error) {
		if e := checked(home, p); e != nil {
			return 0, e
		}
		v, e := os.Stat(p)
		if e != nil {
			return 0, e
		}
		return v.Size(), nil
	}, ReadFile: lf.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if e != nil {
		return e
	}
	allowed := map[string]bool{}
	for p := home; ; p = filepath.Dir(p) {
		allowed[filepath.Join(p, ".git")] = true
		if p == filepath.Dir(p) {
			break
		}
	}
	stat := func(p string) (fs.FileInfo, error) {
		if !allowed[p] {
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
	git, e := gitbranch.New(gitbranch.Options{Owner: owner, CWD: func() string { return state.Snapshot().CWD }, Now: func() int64 { return time.Now().UnixMilli() }, StatSync: stat, Stat: stat, ReadFile: read, WatchFile: watch.Watch, ScrollDraining: func() bool { return false }, RegisterCleanup: func(func(*cooperative.Scope) error) {}, NodeEnv: "production"})
	if e != nil {
		return e
	}
	// Actual no-repo never registers a watcher. These administrative closures do
	// not run any program-end cleanup Registry or join detached snapshot records.
	defer func() { _ = git.Close(context.Background()); watch.Join() }()
	sink, e := sessioncontroller.NewFileHistorySink(pending, loader)
	if e != nil {
		return e
	}
	copy, e := filehistorycopy.New(filehistorycopy.Options{Owner: owner, Binding: pending.NativeCopyBinding(), Sink: sink, Files: native{home}, Environment: func(k string) string { return envMap[k] }, NonInteractive: func() bool { return true }, ConfigHome: func() string { return configDir }, SessionID: state.SessionID, OnError: onError, Debug: func(v string) { debug = append(debug, v) }, Event: func(v string, o protocol.JSONObject) { o.Set("name", quote(v)); events = append(events, o) }})
	if e != nil {
		return e
	}

	factory, e := messages.NewFactory(actualUUID, time.Now)
	if e != nil {
		return e
	}
	reducer, e := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(raw json.RawMessage) (json.RawMessage, error) { return sessionresume.RelativePath(cwd, home, raw) }, OnDiagnostic: func(d sessionresume.Diagnostic) { debug = append(debug, d.Name) }, OnError: onError})
	if e != nil {
		return e
	}
	resume, e := resumestate.NewOwned(resumestate.Options{ConfigDir: configDir, CWD: cwd, OriginalCWD: cwd, CurrentSessionID: sid, NonInteractive: true, Environment: envMap, Files: resumeNative{home}, Stat: func(path string) (resumestate.Stat, error) {
		if e := checked(home, path); e != nil {
			return resumestate.Stat{}, e
		}
		v, e := os.Stat(path)
		if e != nil {
			return resumestate.Stat{}, e
		}
		return resumestate.Stat{Size: v.Size(), Modified: float64(v.ModTime().UnixMilli())}, nil
	}, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Now: func() int64 { return time.Now().UnixMilli() }, UUID: actualUUID, OnDiagnostic: func(d sessionresume.Diagnostic) { debug = append(debug, d.Name) }, OnError: onError, OnPhase: func(string) {}, PolicyPaths: []string{filepath.Join(home, "policy/managed-settings.json")}, SettingsState: settings}, state, startup)
	if e != nil {
		return e
	}
	if e = resume.BindOwnedFileHistory(copy); e != nil {
		return e
	}
	if e = resume.BindOwnedNative(pending.NativeCopyBinding(), func(scope *cooperative.Scope, sid string, t *sessionhistory.Transcript) error {
		_, _, e := pending.PrimeSessionUUIDCache(scope, loader, sid, t)
		return e
	}); e != nil {
		return e
	}
	originalLog, e := resumestate.DecodeOriginalLog(rawLog)
	if e != nil {
		return e
	}
	var returned *resumestate.Result
	// Source originalLog shallow spread retains the input log snapshot-array object.
	// Its messages array is independently mapped, while replacing snapshots later
	// does not change this initial array binding held by the input observer.
	inputSnapshots := originalLog.History.Snapshots
	trace := []protocol.JSONObject{}
	var result []byte
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if prime {
			user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: json.RawMessage(`{"role":"user","content":"original explicit prime Project program input"}`)}, protocol.JSONField{Name: "uuid", Value: quote("cccccccc-cccc-4ccc-8ccc-cccccccccccc")}, protocol.JSONField{Name: "timestamp", Value: quote("2026-01-01T00:00:00.000Z")})
			token, e := state.Context(scope)
			if e != nil {
				return e
			}
			f, e := pending.BeginRecordLive(scope, loader, sessioncontroller.LiveRecordProfile{QuerySource: "sdk"}, []sessioncontroller.LiveRecord{{Record: &user}}, sessioncontroller.RecordOptions{PromptID: func() json.RawMessage { return quote("original-file-history-program-prompt") }, CWDContext: token, Branch: func(next *cooperative.Scope) (sessioncontroller.RecordBranchFuture, error) {
				return git.BranchWithCWD(next, func(next *cooperative.Scope) (string, error) { return token.GetCWD(next) })
			}})
			if e != nil {
				return e
			}
			if _, e = f.Await(scope); e != nil {
				return e
			}
			fl, e := pending.Flush(scope)
			if e != nil {
				return e
			}
			if e = fl.Await(scope); e != nil {
				return e
			}
		}
		before, e := physical(home)
		if e != nil {
			return e
		}
		b, _ := before.MarshalJSON()
		trace = append(trace, protocol.NewJSONObject(protocol.JSONField{Name: "label", Value: quote("before-copy")}, protocol.JSONField{Name: "physical", Value: b}))
		if field(original, "program") == "helper" {
			future, e := copy.Begin(scope, log)
			if e != nil {
				return e
			}
			if _, e = future.Await(scope); e != nil {
				return e
			}
		} else {
			future, e := resume.BeginLoad(scope, resumestate.Input{Original: originalLog})
			if e != nil {
				return e
			}
			if mutation := original.Get("mutation"); mutation != nil {
				var operation protocol.JSONObject
				if e = json.Unmarshal(mutation, &operation); e != nil {
					return e
				}
				container := rawLog.Clone()
				container.Set("fileHistorySnapshots", operation.Get("items"))
				decoded, e := filehistorycopy.DecodeLog(container)
				if e != nil {
					return e
				}
				captured := originalLog.History.Snapshots
				_, e = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
					if field(operation, "kind") == "queued-replace" {
						originalLog.ReplaceSnapshots(decoded.Snapshots)
					} else if field(operation, "kind") == "queued-mutate-captured" {
						captured.Items = decoded.Snapshots.Items
					} else {
						return errors.New("original declared queued array operation required")
					}
					return nil
				})
				if e != nil {
					return e
				}
			}
			returned, e = future.Await(scope)
			if e != nil {
				return e
			}
		}
		after, e := physical(home)
		if e != nil {
			return e
		}
		b, _ = after.MarshalJSON()
		trace = append(trace, protocol.NewJSONObject(protocol.JSONField{Name: "label", Value: quote("immediate-public-return")}, protocol.JSONField{Name: "physical", Value: b}))
		if after := original.Get("afterReturn"); after != nil {
			var action protocol.JSONObject
			if e = json.Unmarshal(after, &action); e != nil {
				return e
			}
			if e = state.SwitchSession(scope, field(action, "sessionId"), nil); e != nil {
				return e
			}
			if field(action, "kind") != "switch" {
				reset, e := writer.BeginResetSessionFile(scope)
				if e != nil {
					return e
				}
				if _, e = reset.Await(scope); e != nil {
					return e
				}
			}
			if field(action, "kind") == "switch-reset-record" {
				var user protocol.JSONObject
				if e = json.Unmarshal(action.Get("user"), &user); e != nil {
					return e
				}
				token, e := state.Context(scope)
				if e != nil {
					return e
				}
				record, e := pending.BeginRecordLive(scope, loader, sessioncontroller.LiveRecordProfile{QuerySource: "sdk"}, []sessioncontroller.LiveRecord{{Record: &user}}, sessioncontroller.RecordOptions{PromptID: func() json.RawMessage { return quote("original-file-history-program-prompt") }, CWDContext: token, Branch: func(next *cooperative.Scope) (sessioncontroller.RecordBranchFuture, error) {
					return git.BranchWithCWD(next, func(next *cooperative.Scope) (string, error) { return token.GetCWD(next) })
				}})
				if e != nil {
					return e
				}
				if _, e = record.Await(scope); e != nil {
					return e
				}
			}
		}
		data, e := json.Marshal(map[string]any{"originalInput": observedInput(original, inputSnapshots), "home": home, "actualPID": os.Getpid(), "trace": trace, "errors": errs, "debug": debug, "events": events, "returned": returned, "returnedIsUndefined": field(original, "program") == "helper", "scope": "actual Go helper/exported public recovery producer with existing Project; print/REPL pending; runtime checkpoint native refs only"})
		result = data
		return e
	})
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(home, "result.json"), result, 0600); e != nil {
		return e
	}
	// Runtime native-work checkpoint is off-owner, not Flush/Close/AwaitBackground
	// and not a join of the pure copy or detached Record producer obligations.
	return pending.WaitRuntimeNative(context.Background())
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
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

func observedInput(original protocol.JSONObject, snapshots *filehistorycopy.SnapshotArray) protocol.JSONObject {
	out := original.Clone()
	if snapshots != nil {
		var log protocol.JSONObject
		_ = json.Unmarshal(original.Get("log"), &log)
		items, _ := json.Marshal(snapshots.Items)
		log.Set("fileHistorySnapshots", items)
		raw, _ := log.MarshalJSON()
		out.Set("log", raw)
	}
	return out
}
