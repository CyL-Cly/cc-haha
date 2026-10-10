package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	diagnosticlog "github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionactivity"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

type original struct {
	ID           string                                                                             `json:"id"`
	Bare         bool                                                                               `json:"bare"`
	SessionID    string                                                                             `json:"sessionID"`
	InitialSlug  string                                                                             `json:"initialSlug"`
	Global       string                                                                             `json:"global"`
	Model        string                                                                             `json:"model"`
	SystemPrompt string                                                                             `json:"systemPrompt"`
	ResponseText string                                                                             `json:"responseText"`
	SDK          []protocol.JSONObject                                                              `json:"sdk"`
	Git          struct{ Kind, Release, FirstHEAD, SecondHEAD, Config, FirstRef, SecondRef string } `json:"git"`
	Clock        struct {
		UnixMillis float64 `json:"unixMillis"`
		Program    string  `json:"program"`
		Controls   []struct {
			PostOrdinal                      int `json:"postOrdinal"`
			HeaderUnixMillis, TailUnixMillis float64
		} `json:"controls"`
	} `json:"clock"`
	SettingsFiles map[string]string `json:"settingsFiles"`
	Env           map[string]string `json:"env"`
	Runtime       map[string]string `json:"runtime"`
}
type input struct {
	Guard    string   `json:"guard,omitempty"`
	Original original `json:"original"`
	Home     string   `json:"home"`
	Origin   string   `json:"origin"`
}

// initializeOriginalProgramSettings is the native fixture program recipe,
// not CLI main/init/preAction. The common production services stay unchanged.
func initializeOriginalProgramSettings(startup *clistartupenv.Service, global *cliglobalconfig.Service, neutral bool) error {
	if neutral {
		if _, err := startup.GetInitialSettings(); err != nil {
			return err
		}
	}
	return global.Enable()
}

func quote(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func inside(home, path string) bool {
	p, e := filepath.Rel(home, path)
	return e == nil && p != ".." && !strings.HasPrefix(p, ".."+string(filepath.Separator)) && !filepath.IsAbs(p)
}
func guard(home, path string) error {
	if !inside(home, path) {
		return errors.New("original private path required")
	}
	for p := path; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink denied")
		}
		c, e := filepath.EvalSymlinks(p)
		if e != nil || !inside(home, c) {
			return errors.New("private canonical path required")
		}
		return nil
	}
}

type files struct{ home string }

func (f files) Open(p string) (sessionstore.File, error) {
	if e := guard(f.home, p); e != nil {
		return nil, e
	}
	return (resumestate.OSFiles{}).Open(p)
}
func (f files) ReadFile(p string) ([]byte, error) {
	if e := guard(f.home, p); e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}
func (f files) WriteFile(p string, b []byte) error {
	if e := guard(f.home, p); e != nil {
		return e
	}
	return (resumestate.OSFiles{}).WriteFile(p, b)
}
func (f files) MkdirAll(p string) error {
	if e := guard(f.home, p); e != nil {
		return e
	}
	return (resumestate.OSFiles{}).MkdirAll(p)
}
func (f files) ReadDir(p string) ([]fs.DirEntry, error) {
	if e := guard(f.home, p); e != nil {
		return nil, e
	}
	return os.ReadDir(p)
}
func (f files) Link(a, b string) error {
	if e := guard(f.home, a); e != nil {
		return e
	}
	if e := guard(f.home, b); e != nil {
		return e
	}
	return os.Link(a, b)
}
func (f files) CopyFile(a, b string) error {
	if e := guard(f.home, a); e != nil {
		return e
	}
	if e := guard(f.home, b); e != nil {
		return e
	}
	return (resumestate.OSFiles{}).CopyFile(a, b)
}

type transcriptFiles struct{ home string }

func (f transcriptFiles) OpenFile(p string, flag int, mode fs.FileMode) (sessioncontroller.TranscriptFile, error) {
	if e := guard(f.home, p); e != nil {
		return nil, e
	}
	return (sessioncontroller.OSFiles{}).OpenFile(p, flag, mode)
}
func (f transcriptFiles) Stat(p string) (fs.FileInfo, error) {
	if e := guard(f.home, p); e != nil {
		return nil, e
	}
	return os.Stat(p)
}
func (f transcriptFiles) ReadFile(p string) ([]byte, error) {
	if e := guard(f.home, p); e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}
func (f transcriptFiles) MkdirAll(p string, m fs.FileMode) error {
	if e := guard(f.home, p); e != nil {
		return e
	}
	return os.MkdirAll(p, m)
}

type chunk struct {
	Bytes []byte
	EOF   bool
	Error error
}

func main() {
	home := os.Getenv("HOME")
	var in input
	must(guard(home, filepath.Join(home, "original-input.json")))
	raw, e := os.ReadFile(filepath.Join(home, "original-input.json"))
	must(e)
	must(json.Unmarshal(raw, &in))
	origin, e := url.Parse(in.Origin)
	must(e)
	canonical, e := filepath.EvalSymlinks(home)
	must(e)
	cwd, e := os.Getwd()
	must(e)
	cwd, e = filepath.EvalSymlinks(cwd)
	must(e)
	if in.Home != home || home != canonical || cwd != home || !in.Original.Bare || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("USERPROFILE") != home || os.Getenv("CC_HAHA_NEUTRAL_SETTINGS_GO") != "1" || os.Getenv("BUN_OPTIONS") != "--no-env-file" || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || os.Getenv("ANTHROPIC_BASE_URL") != in.Origin || os.Getenv("ANTHROPIC_API_KEY") != "private-original-no-quota-key" || os.Getenv("CLAUDE_CODE_ENTRYPOINT") != "sdk" {
		panic("exact original private SDK Go input required")
	}
	o := in.Original
	for relative, raw := range o.SettingsFiles {
		if relative != ".claude/settings.json" {
			panic("undeclared original settings path")
		}
		b, e := os.ReadFile(filepath.Join(home, relative))
		must(e)
		if string(b) != raw {
			panic("original settings bytes differ")
		}
	}
	if _, present := o.SettingsFiles[".claude/settings.json"]; !present {
		if _, e := os.Stat(filepath.Join(home, ".claude/settings.json")); !errors.Is(e, os.ErrNotExist) {
			panic("original absent settings differ")
		}
	}

	for k, v := range o.Env {
		if os.Getenv(k) != v {
			panic("original env differs")
		}
	}
	globalBytes, e := os.ReadFile(filepath.Join(home, ".claude/.claude.json"))
	must(e)
	if string(globalBytes) != o.Global {
		panic("original global bytes differ")
	}
	uuid := func() string {
		b := make([]byte, 16)
		_, e := rand.Read(b)
		must(e)
		b[6] = b[6]&15 | 64
		b[8] = b[8]&63 | 128
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	}
	var clock atomic.Int64
	clock.Store(int64(o.Clock.UnixMillis))
	var clockMu sync.Mutex
	clockCalls := []any{}
	externalControls := []any{}
	now := func() time.Time { return time.UnixMilli(clock.Load()).UTC() }
	nowMillis := func() float64 {
		v := float64(clock.Load())
		clockMu.Lock()
		clockCalls = append(clockCalls, map[string]any{"operation": "Date.now", "unixMillis": v})
		clockMu.Unlock()
		return v
	}
	diagnosticBase := filepath.Join(home, ".claude/diagnostics/cli-events")
	if os.Getenv("CLAUDE_CODE_DIAGNOSTICS_FILE") != diagnosticBase {
		panic("original diagnostic path required")
	}
	processDiagnostics, e := diagnosticlog.NewProcess(diagnosticlog.Options{Environment: os.Getenv, Now: now, NowMillis: func() int64 { return int64(nowMillis()) }, PID: os.Getpid(), Platform: "darwin", Files: privateDiagnostics{home: home}})
	must(e)
	owner := cooperative.New()
	defer owner.Close()
	state, e := bootstrapstate.New(owner, o.SessionID, home, func() (string, error) { return uuid(), nil })
	must(e)
	factory, e := messages.NewFactory(uuid, now)
	must(e)
	settingState, e := clisettings.NewState(nowMillis)
	must(e)
	private := files{home}
	diagnostics := []any{}
	errorsObserved := []string{}
	phases := []string{}
	phase := func(s string) { phases = append(phases, s) }
	onError := func(e error) { errorsObserved = append(errorsObserved, e.Error()) }
	reducer, e := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(r json.RawMessage) (json.RawMessage, error) { return sessionresume.RelativePath(home, home, r) }, OnDiagnostic: func(d sessionresume.Diagnostic) { diagnostics = append(diagnostics, d) }, OnError: onError})
	must(e)
	loader, e := sessionhistory.New(sessionhistory.Options{Files: private, Stat: func(p string) (int64, error) {
		if e := guard(home, p); e != nil {
			return 0, e
		}
		i, e := os.Stat(p)
		if e != nil {
			return 0, e
		}
		return i.Size(), nil
	}, ReadFile: private.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return true }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	must(e)
	config := filepath.Join(home, ".claude")
	settings, e := clisettings.NewService(clisettings.Options{State: settingState, ConfigHome: config, OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: nowMillis, PID: os.Getpid()})
	must(e)
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: now, NowMillis: nowMillis, Diagnostics: processDiagnostics})
	must(e)
	defer global.Close()
	env := map[string]string{"ANTHROPIC_BASE_URL": in.Origin, "ANTHROPIC_API_KEY": "private-original-no-quota-key", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_ENTRYPOINT": os.Getenv("CLAUDE_CODE_ENTRYPOINT")}
	for k, v := range o.Env {
		env[k] = v
	}
	for k, v := range o.Runtime {
		if os.Getenv(k) != v {
			panic("original runtime differs")
		}
		env[k] = v
	}
	env["CLAUDE_CODE_DIAGNOSTICS_FILE"] = diagnosticBase
	envObject := protocol.NewJSONObject()
	envObject.Set("HOME", quote(home))
	envObject.Set("CLAUDE_CONFIG_DIR", quote(config))
	for k, v := range env {
		envObject.Set(k, quote(v))
	}
	environment, e := clistartupenv.NewEnvironment(envObject)
	must(e)
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Diagnostics: processDiagnostics, NowMillis: nowMillis, Settings: settings, SettingsState: settingState, Environment: environment, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	must(e)
	must(initializeOriginalProgramSettings(startup, global, os.Getenv("CC_HAHA_NEUTRAL_LEGACY") != "1"))
	tlsState, e := tlsconfig.NewState(private.ReadFile)
	must(e)
	absent := map[string]bool{}
	for d := filepath.Dir(home); ; d = filepath.Dir(d) {
		absent[filepath.Join(d, ".git")] = true
		if d == filepath.Dir(d) {
			break
		}
	}
	registry, e := runtimecleanup.New(owner)
	must(e)
	activity, e := sessionactivity.NewCallbackNull(owner, registry, processDiagnostics)
	must(e)
	worker, e := sessionstate.New(owner, sessionstate.Options{EmitEvents: func() bool {
		value := strings.ToLower(strings.TrimSpace(os.Getenv("CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS")))
		return value == "1" || value == "true" || value == "yes" || value == "on"
	}})
	must(e)
	shuttingDown := false
	// These original bare inputs have no task admissions; keep an actual fixture
	// state store and query it only at the runtime/cleanup boundaries.
	tasks := []sdkhost.BackgroundTask{}

	gitStat := func(p string) (fs.FileInfo, error) {
		if absent[p] {
			return nil, fs.ErrNotExist
		}
		if e := guard(home, p); e != nil {
			return nil, e
		}
		return os.Stat(p)
	}
	watch := &gitbranch.NativePollWatch{Stat: gitStat}
	gitOwner := owner
	if in.Guard == "foreign-binding" {
		gitOwner = cooperative.New()
		defer gitOwner.Close()
	}
	git, e := gitbranch.New(gitbranch.Options{Owner: gitOwner, Diagnostics: processDiagnostics, CWD: func() string { return home }, Now: func() int64 { return int64(nowMillis()) }, StatSync: gitStat, Stat: gitStat, ReadFile: private.ReadFile, WatchFile: watch.Watch, ScrollDraining: func() bool { return false }, NodeEnv: "production", RegisterCleanupOwned: func(scope *cooperative.Scope, cleanup func(*cooperative.Scope) error) error {
		_, e := registry.Register(scope, runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) { return nil, cleanup(next) }))
		return e
	}})
	must(e)
	branch, e := sessioncontroller.NewOwnedGitRecordBranch(state, git)
	if in.Guard == "foreign-binding" {
		var refused *sessioncontroller.CapabilityError
		if !errors.As(e, &refused) || branch != nil {
			fmt.Fprintln(os.Stderr, "foreign binding was not rejected with typed capability", e)
			os.Exit(1)
		}
		receipt := map[string]any{"guard": in.Guard, "type": "sessioncontroller.CapabilityError", "boundary": refused.Boundary, "error": refused.Error(), "controllerConstructed": false, "startupPhases": phases, "actualPID": os.Getpid(), "actualDate": now().Format(time.RFC3339Nano)}
		b, writeErr := json.Marshal(receipt)
		if writeErr == nil {
			writeErr = os.WriteFile(filepath.Join(home, "admission-refusal.json"), b, 0600)
		}
		if writeErr != nil {
			fmt.Fprintln(os.Stderr, writeErr)
			os.Exit(1)
		}
		fmt.Println(string(b))
		if closeErr := git.Close(context.Background()); closeErr != nil {
			fmt.Fprintln(os.Stderr, closeErr)
			os.Exit(1)
		}
		return
	}
	must(e)
	var originalUserContext *usercontext.Service
	var publicStartup *clistartupenv.Service
	if os.Getenv("CC_HAHA_NEUTRAL_LEGACY") != "1" {
		originalUserContext, e = usercontext.New(usercontext.Options{Environment: environment, Settings: settings, SettingsState: settingState, Now: now, NowMillis: nowMillis, Diagnostics: processDiagnostics, Profile: usercontext.ExternalProfile})
		must(e)
		publicStartup = startup
	}
	controller, e := sessioncontroller.New(sessioncontroller.Options{UserContext: originalUserContext, Diagnostics: processDiagnostics, NowMillis: nowMillis, PublicResumeStartup: publicStartup, Bootstrap: state, AsyncPersistence: true, RecordBranchProfile: sessioncontroller.OwnedGitRecordProfile, BeginOwnedRecordBranch: branch, ResumeOptions: resumestate.Options{ConfigDir: config, CWD: home, OriginalCWD: home, CurrentSessionID: o.SessionID, NonInteractive: true, Environment: map[string]string{"HOME": home, "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN": o.Env["CLAUDE_CODE_RESUME_INTERRUPTED_TURN"]}, Files: private, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Stat: func(p string) (resumestate.Stat, error) {
		if e := guard(home, p); e != nil {
			return resumestate.Stat{}, e
		}
		i, e := os.Stat(p)
		if e != nil {
			return resumestate.Stat{}, e
		}
		return resumestate.Stat{Size: i.Size(), Modified: float64(i.ModTime().UnixMilli())}, nil
	}, Now: func() int64 { return now().UnixMilli() }, UUID: uuid, OnDiagnostic: func(d sessionresume.Diagnostic) { diagnostics = append(diagnostics, d) }, OnError: onError, OnPhase: func(string) {}, PolicyPaths: []string{filepath.Join(home, "policy/managed-settings.json")}, SettingsState: settingState}, WriterOptions: sessioncontroller.WriterOptions{ConfigDir: config, CWD: home, SessionID: o.SessionID, Version: "original-sdk-git-fixture", Environment: map[string]string{"HOME": home, "NODE_ENV": "production", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_ENTRYPOINT": os.Getenv("CLAUDE_CODE_ENTRYPOINT")}, SettingsState: settingState, Files: transcriptFiles{home}, OnError: onError, Schedule: sessioncontroller.RealSchedule, Now: now}, Factory: factory, UUID: uuid, Now: now, OnPhase: phase, ReplayUserMessages: true, BaseToolsCLI: []string{""}, CustomSystemPrompt: quote(o.SystemPrompt)})
	must(e)
	var runErr error
	var printExitCode int
	var computedPath string
	var history []json.RawMessage
	var generationSnapshot protocol.JSONObject
	var accountingSnapshot, durationSnapshot protocol.JSONObject
	var outputMu sync.Mutex
	outputs := [][]byte{}
	wake := make(chan struct{}, 1)
	finished := make(chan struct{})
	physicalDone := make(chan struct{})
	go func() {
		defer close(physicalDone)
		index := 0
		for {
			outputMu.Lock()
			var b []byte
			if index < len(outputs) {
				b = append([]byte(nil), outputs[index]...)
				index++
			}
			outputMu.Unlock()
			if b != nil {
				_, _ = os.Stdout.Write(append(b, '\n'))
				continue
			}
			select {
			case <-wake:
			case <-finished:
				return
			}
		}
	}()
	var h *sdkhost.Host
	var initialSettingsObservation, finalSettingsObservation any
	var firstSettings *protocol.JSONObject
	var firstCache *clisettings.ParsedSettings
	observeSettings := func(scope *cooperative.Scope) (any, error) {
		settings, permission, e := controller.SDKSettingsOnOwner(scope)
		if e != nil {
			return nil, e
		}
		cache := settingState.GetSessionSettingsCache()
		if firstSettings == nil {
			firstSettings = settings
			firstCache = cache
		}
		raw, e := settings.MarshalJSON()
		if e != nil {
			return nil, e
		}
		raw, e = protocol.ParseAndStringifyJavaScriptJSON(raw)
		if e != nil {
			return nil, e
		}
		return map[string]any{"rawAppSettingsJSON": string(raw), "sameSettingsReference": cache != nil && cache.Settings == settings, "sameSessionCacheReference": cache == firstCache, "sameAppSettingsReference": settings == firstSettings, "permissionResult": map[string]any{"toolPermissionContext": permission, "warnings": []any{}, "dangerousPermissions": []any{}, "overlyBroadBashPermissions": []any{}}}, nil
	}

	runErr = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := state.SetOriginalCWD(scope, home); e != nil {
			return e
		}
		if e := state.SetCWD(scope, home); e != nil {
			return e
		}
		cache, e := state.PlanSlugCache(scope)
		if e != nil {
			return e
		}
		if e = cache.Set(scope, o.SessionID, o.InitialSlug); e != nil {
			return e
		}
		hostOptions := sdkhost.Options{Activity: activity, RunState: &sdkhost.RunStateOptions{Registry: registry, Worker: worker, GetTasks: func(*cooperative.Scope) ([]sdkhost.BackgroundTask, error) {
			return append([]sdkhost.BackgroundTask(nil), tasks...), nil
		}, IsShuttingDown: func() bool { return shuttingDown }}, Controller: controller, Diagnostics: processDiagnostics, Owner: owner, Factory: factory, UUID: uuid, Now: now, NowMillis: nowMillis, Global: global, Startup: startup, Settings: settings, SettingsState: settingState, Environment: environment, Model: o.Model, Version: "original-sdk-git-fixture", CWD: home, OriginalEnv: env, Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}, TLSState: tlsState, OnPhase: phase, Output: sdkhost.Output{Enqueue: func(b []byte) error {
			outputMu.Lock()
			outputs = append(outputs, append([]byte(nil), b...))
			outputMu.Unlock()
			select {
			case wake <- struct{}{}:
			default:
			}
			return nil
		}}, ObserveRequest: func(string, http.Header, json.RawMessage) {}}
		if os.Getenv("CC_HAHA_NEUTRAL_LEGACY") == "1" {
			h, e = sdkhost.New(hostOptions)
		} else {
			h, e = sdkhost.NewOwnedNeutral(scope, hostOptions)
		}

		if e != nil {
			return e
		}
		if os.Getenv("CC_HAHA_NEUTRAL_LEGACY") != "1" {
			initialSettingsObservation, e = observeSettings(scope)
			if e != nil {
				return e
			}
		}
		fresh, e := controller.BeginActivateFreshOwned(scope, sessioncontroller.FreshBareSDKProfile)
		if e != nil {
			return e
		}
		if _, e = fresh.Await(scope); e != nil {
			return e
		}
		if _, e = registry.Register(scope, runtimecleanup.NewCallback(h.StartCleanup)); e != nil {
			return e
		}
		pendingInput := ""
		controlInput := func(line string) (bool, error) {
			var control struct {
				Type        string  `json:"type"`
				PostOrdinal int     `json:"postOrdinal"`
				Stage       string  `json:"stage"`
				UnixMillis  float64 `json:"unixMillis"`
			}
			if e := json.Unmarshal([]byte(line), &control); e != nil {
				return false, e
			}
			if control.Type != "fixture_clock_control" {
				return false, nil
			}
			if o.Clock.Program != "public-request-and-stream-delta" {
				return false, errors.New("undeclared original clock control")
			}
			matched := false
			for _, spec := range o.Clock.Controls {
				if spec.PostOrdinal == control.PostOrdinal {
					want := spec.HeaderUnixMillis
					if control.Stage == "tail" {
						want = spec.TailUnixMillis
					} else if control.Stage != "header" {
						break
					}
					matched = control.UnixMillis == want
				}
			}
			if !matched {
				return false, errors.New("original clock control differs")
			}
			clock.Store(int64(control.UnixMillis))
			externalControls = append(externalControls, json.RawMessage(line))
			if e := os.WriteFile(filepath.Join(home, fmt.Sprintf("clock-receipt-%d-%s.json", control.PostOrdinal, control.Stage)), []byte(line), 0600); e != nil {
				return false, e
			}
			return true, nil
		}
		var read sdkhost.GeneratorContinuation[string]
		read = func(scope *cooperative.Scope, it *sdkhost.OwnerIterator[string], req sdkhost.IteratorRequest[string]) error {
			if req.Returning {
				return it.CompleteReturn(scope)
			}
			future, e := sdkhost.NewOwnerFuture[chunk](owner)
			if e != nil {
				return e
			}
			go func() {
				b := make([]byte, 65536)
				n, e := os.Stdin.Read(b)
				value := chunk{Bytes: append([]byte(nil), b[:n]...), EOF: errors.Is(e, io.EOF), Error: e}
				_, _ = owner.SubmitIndependent(context.Background(), cooperative.Input, func(next *cooperative.Scope) error { return future.Resolve(next, value) })
			}()
			return sdkhost.AwaitGenerator(scope, it, future, func(next *cooperative.Scope, value chunk) error {
				if value.Error != nil && !value.EOF {
					return value.Error
				}
				pendingInput += string(value.Bytes)
				sdk := ""
				for {
					end := strings.IndexByte(pendingInput, '\n')
					if end < 0 {
						break
					}
					line := pendingInput[:end]
					pendingInput = pendingInput[end+1:]
					if line == "" {
						continue
					}
					consumed, e := controlInput(line)
					if e != nil {
						return e
					}
					if !consumed {
						sdk += line + "\n"
					}
				}
				if sdk != "" {
					return it.Yield(next, sdk, read)
				}
				if value.EOF {
					if pendingInput != "" {
						consumed, e := controlInput(pendingInput)
						if e != nil {
							return e
						}
						if !consumed {
							return it.Yield(next, pendingInput, read)
						}
					}
					return it.Complete(next)
				}
				return read(next, it, req)
			}, nil)
		}
		raw, e := sdkhost.NewOwnerIterator(owner, read)
		if e != nil {
			return e
		}
		structured, e := sdkhost.NewPlainStructuredInputWithDiagnostics(owner, raw, processDiagnostics)
		if e != nil {
			return e
		}
		e = h.RunStream(scope, context.Background(), structured, sdkhost.StreamOptions{})
		if e != nil {
			return e
		}
		// Source print.ts:1015 consumes the last delivered result.is_error.
		// This finite caller policy is independent of fixture IDs and joins;
		// it does not implement native rejection turns.
		printExitCode = h.ExitCode()
		shuttingDown = true
		cleanup, e := registry.Start(scope)
		if e != nil {
			return e
		}
		if _, e = cleanup.Await(scope); e != nil {
			return e
		}
		if os.Getenv("CC_HAHA_NEUTRAL_LEGACY") != "1" {
			finalSettingsObservation, e = observeSettings(scope)
			if e != nil {
				return e
			}
		}
		ledger, e := controller.ModelAccountingFor(owner)
		if e != nil {
			return e
		}
		generationSnapshot, e = ledger.GenerationSnapshot(scope)
		if e != nil {
			return e
		}
		// Observation only: read the existing same-Owner ledger at the original
		// generation observation boundary, without another await or clock read.
		accountingSnapshot, e = ledger.Snapshot(scope)
		if e != nil {
			return e
		}
		durationSnapshot, e = ledger.DurationSnapshot(scope)
		if e != nil {
			return e
		}
		computedPath, e = controller.Writer().ComputedTranscriptPath(scope)
		if e != nil {
			return e
		}
		for _, m := range h.History() {
			b, e := m.MarshalJSON()
			if e != nil {
				return e
			}
			history = append(history, b)
		}
		return nil
	})
	close(finished)
	<-physicalDone
	result := map[string]any{"accountingSnapshot": accountingSnapshot, "durationSnapshot": durationSnapshot, "generation": generationSnapshot, "originalClockCalls": clockCalls, "externalClockControls": externalControls, "scope": "actual SDK Go producer and unique model/persistence services; raw original input, not Source output", "history": history, "bootstrap": state.Snapshot(), "computedTranscriptPath": computedPath, "phases": phases, "diagnostics": diagnostics, "errors": errorsObserved, "outputs": outputs, "runError": nil, "originalEnvironment": env, "initialSettingsObservation": initialSettingsObservation, "finalSettingsObservation": finalSettingsObservation, "actualPID": os.Getpid(), "actualWallDate": now().Format(time.RFC3339Nano)}
	if runErr != nil {
		result["runError"] = runErr.Error()
	}
	result["printExitCode"] = printExitCode
	b, e := json.Marshal(result)
	must(e)
	must(os.WriteFile(filepath.Join(home, "go-exit-observation.json"), b, 0600))
	if h != nil {
		_ = h.Join(context.Background())
	}
	_ = git.Close(context.Background())
	watch.Join()
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
	if printExitCode != 0 {
		os.Exit(printExitCode)
	}
}
