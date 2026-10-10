package main

import (
	"context"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type input struct {
	Home, Origin, Temporary, SID, ResumeAt, Model              string
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

type transcriptFiles struct{ home string }

func (f transcriptFiles) OpenFile(path string, flag int, mode fs.FileMode) (sessioncontroller.TranscriptFile, error) {
	must(guard(f.home, path, true))
	return (sessioncontroller.OSFiles{}).OpenFile(path, flag, mode)
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
	if os.Getenv("CC_HAHA_SESSION_CONTROLLER_FIXTURE") != "1" || os.Getenv("NODE_ENV") != "production" || os.Getenv("TZ") != "UTC" || input.Stat == nil || input.Model == "" {
		panic("headless requires original finite offline inputs")
	}
	origin, err := url.Parse(input.Origin)
	must(err)
	if origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || os.Getenv("ANTHROPIC_BASE_URL") != input.Origin || os.Getenv("ANTHROPIC_API_KEY") != "offline-fixture-key" {
		panic("exact private loopback origin required")
	}
	policy := filepath.Join(input.Home, "policy", "managed-settings.json")
	output := filepath.Join(input.Home, "controller-result.json")
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
			counter = 0
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
	owner := cooperative.New()
	defer owner.Close()
	bootstrap, err := bootstrapstate.New(owner, input.Temporary, input.Home, func() (string, error) { return uuid(), nil })
	must(err)
	// This corpus retains the finite component resume profile and first HTTP
	// boundary. Only cases with an ASK need the canonical independent Record
	// consumer; the no-ASK checkpoint case keeps its original background-copy
	// producer. Full public resume/SessionStart is covered by CLI consumer tests.
	controller, err := sessioncontroller.New(sessioncontroller.Options{Bootstrap: bootstrap, AsyncPersistence: input.WriterOnly == "" && len(input.SDK) > 0, RecordBranchProfile: sessioncontroller.LegacySyncRecordProfile, ResumeOptions: resumeOptions, WriterOptions: sessioncontroller.WriterOptions{ConfigDir: filepath.Join(input.Home, ".claude"), CWD: input.Home, SessionID: input.Temporary, Version: "fixture-version", Environment: environment, SessionPersistenceDisabled: input.Disabled, SettingsState: state, Files: transcriptFiles{input.Home}, Branch: branch, OnError: onError, Schedule: sessioncontroller.RealSchedule, Now: now}, Factory: factory, UUID: uuid, Now: now, OnPhase: phase, ReplayUserMessages: true, BaseToolsCLI: []string{""}, CustomSystemPrompt: json.RawMessage(`"original custom system"`)})
	must(err)
	if input.WriterOnly != "" {
		runWriter(input, controller.Writer(), owner, bootstrap)
		return
	}
	var selection json.RawMessage
	if !input.Continue {
		selection = []byte(source(input.SID))
	}
	must(owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return controller.ActivateOwned(scope, sessioncontroller.Activation{Resume: resumestate.Input{Source: selection}, ForkSession: input.ForkSession, ResumeAt: input.ResumeAt})
	}))
	background := map[string]bool{}
	if input.HoldCopies {
		background["returnedBeforeCopy"] = controller.ResumeReturnedSnapshotCount() == 0
	}
	activationHistory := controller.History()
	appState := controller.AppState()
	cache := controller.ReadCache().Entries()
	uuidChecks := []map[string]any{}

	ask, err := sessioncontroller.NewSerialASK(controller, owner)
	must(err)
	pending, err := controller.Writer().PendingWriter(owner)
	must(err)
	defer func() { must(pending.Close(context.Background())) }()
	admissions := []sessioncontroller.Admission{}
	must(owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, sdk := range input.SDK {
			future, err := ask.AdmitParsedSDK(scope, sdk)
			if err != nil {
				return err
			}
			admission, err := future.Await(scope)
			if err != nil {
				return err
			}
			admissions = append(admissions, admission)
		}
		return nil
	}))
	for index, sdk := range input.SDK {
		if sdk.Get("uuid") != nil && string(sdk.Get("uuid")) != `""` {
			uuidChecks = append(uuidChecks, map[string]any{"uuid": sdk.Get("uuid"), "exists": admissions[index].Historical})
		}
	}

	var accepted *protocol.JSONObject
	must(owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		accepted, err = ask.BeginDequeued(scope)
		if err != nil || accepted == nil || input.Disabled {
			return err
		}
		phase("record")
		return ask.Record(scope, ask.History())
	}))
	requests := []string{}
	if accepted != nil {
		hostEnv := map[string]string{"ANTHROPIC_BASE_URL": input.Origin, "ANTHROPIC_API_KEY": "offline-fixture-key", "ANTHROPIC_CUSTOM_HEADERS": "x-client-request-id: original-fixture-trace"}
		prepared, err := controller.Prepare(requestcontext.Context{Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: input.Model}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: hostEnv, DeviceID: "fixture-controller-device", SDKBetas: []string{}, TTLAllowlist: []string{}}}, requestcontext.Dependencies{UUID: uuid, Now: now})
		must(err)
		request, err := prepared.BuildAttempt(requestcontext.AttemptOptions{Model: input.Model})
		must(err)
		requests = append(requests, string(request.PreparedRequest.Body))
		key := "offline-fixture-key"
		resolved := provider.ResolveDirectClientOptions(provider.DirectClientInput{Env: hostEnv, FallbackAPIKey: &key, SessionID: controller.SessionID(), Runtime: input.Runtime, RequestID: uuid})
		config := resolved.StreamConfig()
		transport := &http.Transport{}
		defer transport.CloseIdleConnections()
		config.HTTPClient = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		finalize := config.FinalizeHeaders
		config.FinalizeHeaders = func(target string, headers http.Header) (http.Header, error) {
			if !strings.HasPrefix(target, input.Origin+"/") {
				return nil, errors.New("headless request escaped origin")
			}
			return finalize(target, headers)
		}
		phase("HTTP")
		stream, err := provider.NewAnthropic(config).OpenMessagesStream(context.Background(), request.PreparedRequest)
		must(err)
		for {
			_, err = stream.Next()
			if err == io.EOF {
				break
			}
			must(err)
		}
		must(stream.Close())
	}
	controller.AwaitBackground()
	must(owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		flush, err := pending.Flush(scope)
		if err != nil {
			return err
		}
		return flush.Await(scope)
	}))
	must(controller.Writer().RefreshMetadata())
	history := controller.History()
	historySource := []string{}
	for _, message := range history {
		historySource = append(historySource, source(message))
	}
	transcript := map[string]string{}
	directory := filepath.Join(input.Home, ".claude", "projects")
	_ = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		must(guard(input.Home, path, false))
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			must(err)
			name, _ := filepath.Rel(input.Home, path)
			transcript[name] = string(data)
		}
		return nil
	})
	boundaryOrder := []string{}
	for _, value := range phases {
		switch value {
		case "resume:start", "resume:return", "switch", "reset", "state:restore", "metadata:restore", "enqueue", "dequeue", "ask:start", "record", "HTTP", "lifecycle:started", "lifecycle:completed":
			boundaryOrder = append(boundaryOrder, value)
		}
	}
	copies := map[string]string{}
	_ = filepath.WalkDir(filepath.Join(input.Home, ".claude", "file-history"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		must(guard(input.Home, path, false))
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			must(err)
			name, _ := filepath.Rel(input.Home, path)
			copies[name] = string(data)
		}
		return nil
	})
	observation := map[string]any{"boundaryOrder": boundaryOrder, "activationHistory": activationHistory, "snapshotRecords": controller.SnapshotRecords(), "background": background, "appState": appState, "cache": cache, "uuidChecks": uuidChecks, "enqueued": controller.QueueEvents(), "replays": controller.Replays(), "historySource": historySource, "requests": requests, "transcripts": transcript, "errors": errorRows, "copies": copies}
	must(os.WriteFile(output, []byte(source(map[string]any{"observation": observation, "nativeOnly": map[string]any{"phases": phases, "diagnostics": diagnosticRows}})), 0600))
	fmt.Println(`{"result":"controller-result.json"}`)
}

func runWriter(input input, writer *sessioncontroller.Writer, owner *cooperative.Owner, bootstrap *bootstrapstate.State) {
	// Original writer fixture switches the process SID before adopting the file.
	// Writer.Switch alone updates its bookkeeping, not the shared Bootstrap SID.
	must(owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return bootstrap.SwitchSession(scope, input.SID, nil)
	}))
	writer.Switch(input.SID, "")
	writer.AdoptSessionFile()
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"queue-operation"`)}, protocol.JSONField{Name: "operation", Value: json.RawMessage(`"enqueue"`)}, protocol.JSONField{Name: "timestamp", Value: json.RawMessage(`"2025-01-01T00:00:00Z"`)}, protocol.JSONField{Name: "sessionId", Value: json.RawMessage(`"11111111-1111-4111-8111-111111111111"`)}, protocol.JSONField{Name: "content", Value: json.RawMessage(`"original adopted queued entry"`)})
	must(writer.Append(&entry))
	path := filepath.Join(input.Home, ".claude", "projects", strings.Map(func(char rune) rune {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			return char
		}
		return '-'
	}, input.Home), input.SID+".jsonl")
	must(guard(input.Home, path, true))
	switch input.WriterOnly {
	case "replace":
		must(guard(input.Home, path+".old", true))
		must(os.Rename(path, path+".old"))
		must(os.WriteFile(path, []byte("replacement-sentinel\n"), 0600))
	case "delete":
		must(os.Remove(path))
	case "missing", "existing":
	default:
		panic("unknown writer profile")
	}
	must(writer.Flush())
	data, err := os.ReadFile(path)
	var value any
	exists := err == nil
	if exists {
		value = string(data)
	} else if !errors.Is(err, fs.ErrNotExist) {
		must(err)
	}
	fmt.Println(source(map[string]any{"observed": map[string]any{"exists": exists, "bytes": value}}))
}
