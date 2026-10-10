package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type input struct {
	Home, Origin, Temporary, SID, ResumeAt, Model, ProbeMode   string
	Continue, ForkSession, Checkpointing, Disabled, HoldCopies bool
	ResumeInterrupted, WriterOnly                              string
	SDK                                                        []protocol.JSONObject
	Stat                                                       map[string]resumestate.Stat
	Runtime                                                    provider.SourceRuntime
}
type privateFiles struct {
	home    string
	release <-chan struct{}
}

func (f privateFiles) checked(path string) { must(guard(f.home, path, false)) }
func (f privateFiles) Open(path string) (sessionstore.File, error) {
	f.checked(path)
	return (resumestate.OSFiles{}).Open(path)
}
func (f privateFiles) ReadFile(path string) ([]byte, error) {
	f.checked(path)
	return os.ReadFile(path)
}
func (f privateFiles) WriteFile(path string, value []byte) error {
	f.checked(path)
	return (resumestate.OSFiles{}).WriteFile(path, value)
}
func (f privateFiles) MkdirAll(path string) error {
	f.checked(path)
	return (resumestate.OSFiles{}).MkdirAll(path)
}
func (f privateFiles) ReadDir(path string) ([]fs.DirEntry, error) {
	f.checked(path)
	return os.ReadDir(path)
}
func (f privateFiles) Link(old, new string) error {
	f.checked(old)
	f.checked(new)
	if f.release != nil {
		<-f.release
	}
	return os.Link(old, new)
}
func (f privateFiles) CopyFile(old, new string) error {
	f.checked(old)
	f.checked(new)
	return (resumestate.OSFiles{}).CopyFile(old, new)
}

type transcriptFiles struct {
	home, origin string
	client       *http.Client
	notified     *sync.Once
}
type observedFile struct {
	sessioncontroller.TranscriptFile
	original transcriptFiles
}

func (f observedFile) Write(b []byte) (int, error) {
	n, err := f.TranscriptFile.Write(b)
	if err == nil && strings.Contains(string(b[:n]), `"uuid":"first-sdk-id"`) {
		f.original.notified.Do(func() {
			response, e := f.original.client.Get(f.original.origin + "/__fixture/first-user-persisted")
			if e != nil {
				panic(e)
			}
			_ = response.Body.Close()
		})
	}
	return n, err
}

func (f transcriptFiles) OpenFile(path string, flag int, mode fs.FileMode) (sessioncontroller.TranscriptFile, error) {
	must(guard(f.home, path, true))
	file, err := (sessioncontroller.OSFiles{}).OpenFile(path, flag, mode)
	if err != nil {
		return nil, err
	}
	return observedFile{file, f}, nil
}
func (f transcriptFiles) Stat(path string) (fs.FileInfo, error) {
	must(guard(f.home, path, true))
	return os.Stat(path)
}
func (f transcriptFiles) ReadFile(path string) ([]byte, error) {
	must(guard(f.home, path, true))
	return os.ReadFile(path)
}
func (f transcriptFiles) MkdirAll(path string, mode fs.FileMode) error {
	must(guard(f.home, path, false))
	return os.MkdirAll(path, mode)
}
func source(value any) string {
	data, err := json.Marshal(value)
	must(err)
	data, err = protocol.ParseAndStringifyJavaScriptJSON(data)
	must(err)
	return string(data)
}
func main() {
	var input input
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	must(decoder.Decode(&input))
	if decoder.Decode(new(any)) != io.EOF {
		panic("single original input required")
	}
	must(assertHome(input.Home))
	if os.Getenv("CC_HAHA_SESSION_CONTROLLER_FIXTURE") != "1" || os.Getenv("NODE_ENV") != "production" || os.Getenv("TZ") != "UTC" || input.Stat == nil || input.Model == "" || !(input.ProbeMode == "finish-after-stop" || input.ProbeMode == "empty-first" || input.ProbeMode == "eof-first" || input.ProbeMode == "usage-fields" || input.ProbeMode == "synthetic-no-response" || input.ProbeMode == "synthetic-cancel" || input.ProbeMode == "synthetic-reject" || input.ProbeMode == "preflight-blocked") {
		panic("headless requires original finite offline inputs")
	}
	origin, err := url.Parse(input.Origin)
	must(err)
	if origin.Scheme != "https" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || os.Getenv("ANTHROPIC_BASE_URL") != input.Origin || os.Getenv("ANTHROPIC_API_KEY") != "offline-fixture-key" {
		panic("exact private loopback origin required")
	}
	policy := filepath.Join(input.Home, "policy", "managed-settings.json")
	output := filepath.Join(input.Home, "sdk-full-ask-result.json")
	must(guard(input.Home, policy, true))
	must(guard(input.Home, output, true))
	for path := range input.Stat {
		must(guard(input.Home, path, true))
	}
	phases := []string{}
	errorRows := []string{}
	var mu sync.Mutex
	scope := "startup"
	counter := 0
	var releaseCopy chan struct{}
	var releaseOnce sync.Once
	phase := func(value string) {
		mu.Lock()
		defer mu.Unlock()
		phases = append(phases, value)
		if value == "read-cache:return" && releaseCopy != nil {
			releaseOnce.Do(func() { close(releaseCopy) })
		}
		if value == "resume:start" {
			scope = "resume"
			counter = 0
		}
		if value == "ask:start" {
			scope = "turn"
		}
	}
	uuid := func() string {
		mu.Lock()
		defer mu.Unlock()
		counter++
		prefix := 1
		if scope == "turn" {
			prefix = 2
		}
		return fmt.Sprintf("00000000-0000-4000-%04d-%012d", prefix, counter)
	}
	now := func() time.Time { return time.UnixMilli(1700000000000).UTC() }
	onError := func(err error) { mu.Lock(); defer mu.Unlock(); errorRows = append(errorRows, err.Error()) }
	diagnosticRows := []sessionresume.Diagnostic{}
	diagnostic := func(value sessionresume.Diagnostic) {
		mu.Lock()
		defer mu.Unlock()
		diagnosticRows = append(diagnosticRows, value)
	}
	factory, err := messages.NewFactory(uuid, now)
	must(err)
	var release chan struct{}
	if input.HoldCopies {
		release = make(chan struct{})
	}
	files := privateFiles{input.Home, release}
	releaseCopy = release
	reducer, err := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(raw json.RawMessage) (json.RawMessage, error) {
		return sessionresume.RelativePath(input.Home, input.Home, raw)
	}, OnDiagnostic: diagnostic, OnError: onError})
	must(err)
	loader, err := sessionhistory.New(sessionhistory.Options{Files: files, Stat: func(path string) (int64, error) {
		files.checked(path)
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}, ReadFile: files.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return true }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	must(err)
	state, err := clisettings.NewState(func() float64 { return 1700000000000 })
	must(err)
	environment := map[string]string{"HOME": input.Home, "NODE_ENV": "production", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING": fmt.Sprint(input.Checkpointing), "CLAUDE_CODE_RESUME_INTERRUPTED_TURN": input.ResumeInterrupted}
	resumeOptions := resumestate.Options{ConfigDir: filepath.Join(input.Home, ".claude"), CWD: input.Home, OriginalCWD: input.Home, CurrentSessionID: input.Temporary, NonInteractive: true, Environment: environment, Files: files, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Stat: func(path string) (resumestate.Stat, error) {
		files.checked(path)
		if _, err := os.Stat(path); err != nil {
			return resumestate.Stat{}, err
		}
		value, exists := input.Stat[path]
		if !exists {
			return resumestate.Stat{}, errors.New("original filesystem stat seam missing")
		}
		return value, nil
	}, Now: func() int64 { return now().UnixMilli() }, UUID: uuid, OnDiagnostic: diagnostic, OnError: onError, OnPhase: func(string) {}, PolicyPaths: []string{policy}, SettingsState: state}
	absent := map[string]bool{}
	for directory := filepath.Dir(input.Home); ; directory = filepath.Dir(directory) {
		absent[filepath.Join(directory, ".git")] = true
		if filepath.Dir(directory) == directory {
			break
		}
	}
	branch := func() (string, error) {
		return sessioncontroller.NoRepositoryBranch(input.Home, func(path string) (fs.FileInfo, error) {
			if absent[path] {
				return nil, fs.ErrNotExist
			}
			must(guard(input.Home, path, false))
			return os.Stat(path)
		})
	}
	cert, e := os.ReadFile(filepath.Join(input.Home, "original-ca.pem"))
	must(e)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		panic("private CA required")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}, Timeout: 10 * time.Second}
	var notified sync.Once
	owner := cooperative.New()
	defer owner.Close()
	bootstrap, err := bootstrapstate.New(owner, input.Temporary, input.Home, func() (string, error) { return uuid(), nil })
	must(err)
	controller, err := sessioncontroller.New(sessioncontroller.Options{Bootstrap: bootstrap, AsyncPersistence: true, RecordBranchProfile: sessioncontroller.LegacySyncRecordProfile, ResumeOptions: resumeOptions, WriterOptions: sessioncontroller.WriterOptions{ConfigDir: filepath.Join(input.Home, ".claude"), CWD: input.Home, SessionID: input.Temporary, Version: "fixture-version", Environment: environment, SessionPersistenceDisabled: input.Disabled, SettingsState: state, Files: transcriptFiles{input.Home, input.Origin, client, &notified}, Branch: branch, OnError: onError, Schedule: sessioncontroller.RealSchedule, Now: now}, Factory: factory, UUID: uuid, Now: now, OnPhase: phase, ReplayUserMessages: true, BaseToolsCLI: []string{""}, CustomSystemPrompt: json.RawMessage(`"original custom system"`)})
	must(err)

	var selection json.RawMessage
	if !input.Continue {
		selection = quote(input.SID)
	}
	must(owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return controller.ActivateOwned(scope, sessioncontroller.Activation{Resume: resumestate.Input{Source: selection}, ForkSession: input.ForkSession, ResumeAt: input.ResumeAt})
	}))
	config := filepath.Join(input.Home, ".claude")
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: input.Home, ConfigDir: &config, Now: now})
	must(e)
	must(global.Enable())
	defer global.Close()
	env := map[string]string{"ANTHROPIC_BASE_URL": input.Origin, "ANTHROPIC_API_KEY": "offline-fixture-key", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "NODE_EXTRA_CA_CERTS": filepath.Join(input.Home, "original-ca.pem")}
	original := protocol.NewJSONObject()
	original.Set("HOME", quote(input.Home))
	original.Set("CLAUDE_CONFIG_DIR", quote(config))
	for key, value := range env {
		original.Set(key, quote(value))
	}
	startupEnv, e := clistartupenv.NewEnvironment(original)
	must(e)
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, SettingsState: state, Settings: mustSettings(state, config, input.Home), Environment: startupEnv, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	must(e)
	tlsState, e := tlsconfig.NewState(func(path string) ([]byte, error) {
		if path != env["NODE_EXTRA_CA_CERTS"] {
			return nil, errors.New("unexpected TLS file")
		}
		must(guard(input.Home, path, true))
		return os.ReadFile(path)
	})
	must(e)
	outputs := [][]byte{}
	requests := []string{}
	originalModelInputs := []string{}
	outputTurn := 0
	var firstResult, observedStop *sdkhost.OwnerFuture[struct{}]
	var inputScope *cooperative.Scope
	// One physical FIFO writer consumes immutable, already owner-serialized SDK
	// bytes. Enqueue does not await physical stdout, matching local headless IO.
	var outputMu sync.Mutex
	wake := make(chan struct{}, 1)
	finished := make(chan struct{})
	physicalDone := make(chan struct{})
	go func() {
		defer close(physicalDone)
		index := 0
		for {
			outputMu.Lock()
			var data []byte
			if index < len(outputs) {
				data = append([]byte(nil), outputs[index]...)
				index++
			}
			outputMu.Unlock()
			if data != nil {
				_, e := os.Stdout.Write(append(data, '\n'))
				if e != nil {
					panic(e)
				}
				continue
			}
			select {
			case <-wake:
			case <-finished:
				return
			}
		}
	}()
	host, e := sdkhost.New(sdkhost.Options{Controller: controller, Owner: owner, Factory: factory, UUID: uuid, Now: now, NowMillis: func() float64 { return 1700000000000 }, Global: global, Startup: startup, Settings: mustSettings(state, config, input.Home), SettingsState: state, Model: input.Model, Version: "fixture-version", CWD: input.Home, OriginalEnv: env, Runtime: input.Runtime, TLSState: tlsState, OnPhase: phase, ObserveModelInput: func(original requestretry.Input) { originalModelInputs = append(originalModelInputs, source(original)) }, ObserveRequest: func(_ string, _ http.Header, body json.RawMessage) { requests = append(requests, string(body)) }, Output: sdkhost.Output{Enqueue: func(data []byte) error {
		outputMu.Lock()
		outputs = append(outputs, append([]byte(nil), data...))
		outputMu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
		event := object(data)
		if event.String("type") == "system" && event.String("subtype") == "init" {
			outputTurn++
		}
		if outputTurn == 1 && event.String("type") == "stream_event" && object(event.Get("event")).String("type") == "content_block_stop" {
			must(observedStop.Resolve(inputScope, struct{}{}))
		}
		if outputTurn == 1 && event.String("type") == "result" {
			must(firstResult.Resolve(inputScope, struct{}{}))
		}
		return nil
	}}})
	must(e)
	var runError error
	history := []string{}
	transcripts := map[string]string{}
	// Retain one real producer stack through serial admission, Project cleanup
	// and the synchronous process-exit observation. Off-owner administrative
	// waits cannot admit extra outer Record work before that observation.
	must(owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		inputScope = scope
		firstResult, e = sdkhost.NewOwnerFuture[struct{}](owner)
		if e != nil {
			return e
		}
		observedStop, e = sdkhost.NewOwnerFuture[struct{}](owner)
		if e != nil {
			return e
		}
		if len(input.SDK) != 2 {
			return errors.New("original finite two-input producer required")
		}
		control := func(scope *cooperative.Scope) (*sdkhost.OwnerFuture[struct{}], error) {
			pending, err := sdkhost.NewOwnerFuture[struct{}](owner)
			if err != nil {
				return nil, err
			}
			go func() {
				response, err := client.Get(input.Origin + "/__fixture/release-final")
				if err == nil {
					_, err = io.ReadAll(response.Body)
					closeErr := response.Body.Close()
					if err == nil {
						err = closeErr
					}
				}
				_, _ = owner.SubmitIndependent(context.Background(), cooperative.Input, func(scope *cooperative.Scope) error {
					if err != nil {
						return pending.Reject(scope, err)
					}
					return pending.Resolve(scope, struct{}{})
				})
			}()
			return pending, nil
		}
		var afterFirst sdkhost.GeneratorContinuation[string]
		afterFirst = func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[string], request sdkhost.IteratorRequest[string]) error {
			if request.Returning {
				return errors.New("original input consumer return is unported")
			}
			waitResult := func(scope *cooperative.Scope) error {
				return sdkhost.AwaitGenerator(scope, iterator, firstResult, func(scope *cooperative.Scope, _ struct{}) error {
					phase("original-SDK:second-after-result")
					return iterator.Yield(scope, source(input.SDK[1])+"\n", func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[string], _ sdkhost.IteratorRequest[string]) error {
						phase("original-SDK:EOF")
						return iterator.Complete(scope)
					})
				}, nil)
			}
			if input.ProbeMode == "empty-first" || input.ProbeMode == "preflight-blocked" {
				return waitResult(scope)
			}
			return sdkhost.AwaitGenerator(scope, iterator, observedStop, func(scope *cooperative.Scope, _ struct{}) error {
				pending, err := control(scope)
				if err != nil {
					return err
				}
				return sdkhost.AwaitGenerator(scope, iterator, pending, func(scope *cooperative.Scope, _ struct{}) error { return waitResult(scope) }, nil)
			}, nil)
		}
		raw, err := sdkhost.NewOwnerIterator(owner, func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[string], _ sdkhost.IteratorRequest[string]) error {
			phase("original-SDK:first")
			return iterator.Yield(scope, source(input.SDK[0])+"\n", afterFirst)
		})
		if err != nil {
			return err
		}
		structured, err := sdkhost.NewPlainStructuredInput(owner, raw)
		if err != nil {
			return err
		}
		runError = host.RunStream(scope, context.Background(), structured, sdkhost.StreamOptions{WrapASK: func(inner *sdkhost.OwnerIterator[[]byte]) (*sdkhost.OwnerIterator[[]byte], error) {
			return sdkhost.NewOwnerIterator(owner, func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[[]byte], request sdkhost.IteratorRequest[[]byte]) error {
				return iterator.Delegate(scope, inner, request, func(scope *cooperative.Scope, _ sdkhost.IteratorResult[[]byte], _ sdkhost.IteratorRequest[[]byte]) error {
					phase("ask:observational-wrapper-finally")
					return iterator.Complete(scope)
				}, func(scope *cooperative.Scope, err error) error {
					phase("ask:observational-wrapper-finally")
					return iterator.Fail(scope, err)
				})
			})
		}})
		if e := host.Shutdown(scope); e != nil {
			return e
		}
		for _, message := range host.History() {
			data, e := message.MarshalJSON()
			if e != nil {
				return e
			}
			history = append(history, string(data))
		}
		return filepath.WalkDir(filepath.Join(config, "projects"), func(path string, entry fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			must(guard(input.Home, path, false))
			if entry.Type().IsRegular() {
				data, e := os.ReadFile(path)
				if e != nil {
					return e
				}
				relative, e := filepath.Rel(input.Home, path)
				if e != nil {
					return e
				}
				transcripts[relative] = string(data)
			}
			return nil
		})
	}))
	close(finished)
	<-physicalDone
	controller.AwaitBackground()
	// Take an immutable observer snapshot without joining or rescheduling Record.
	mu.Lock()
	phaseSnapshot := append([]string{}, phases...)
	errorSnapshot := append([]string{}, errorRows...)
	diagnosticSnapshot := append([]sessionresume.Diagnostic{}, diagnosticRows...)
	mu.Unlock()
	result := map[string]any{"historySource": history, "transcripts": transcripts, "requests": requests, "originalModelInputs": originalModelInputs, "phases": phaseSnapshot, "cache": controller.ReadCache().Entries(), "appState": controller.AppState(), "errors": errorSnapshot, "diagnostics": diagnosticSnapshot, "runError": nil}
	if runError != nil {
		result["runError"] = map[string]any{"type": fmt.Sprintf("%T", runError), "message": runError.Error()}
	}
	must(os.WriteFile(output, []byte(source(result)), 0600))
	if runError != nil {
		panic(runError)
	}
	os.Exit(host.ExitCode())
}
func quote(value string) json.RawMessage { return json.RawMessage(source(value)) }
func object(value []byte) protocol.JSONObject {
	var o protocol.JSONObject
	must(o.UnmarshalJSON(value))
	return o
}
func mustSettings(state *clisettings.State, config, home string) *clisettings.Service {
	s, e := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1700000000000 }, PID: os.Getpid()})
	must(e)
	return s
}
