package sdkhost_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
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
	home, origin     string
	client           *http.Client
	notified         *sync.Once
	assistantWritten func()
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
	if err == nil && f.original.assistantWritten != nil {
		for _, line := range bytes.Split(b[:n], []byte("\n")) {
			var record protocol.JSONObject
			if record.UnmarshalJSON(line) != nil || record.String("type") != "assistant" {
				continue
			}
			if message, ok := record.Object("message"); ok && message.String("id") == "probe-first" {
				f.original.assistantWritten()
			}
		}
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

type nativeControls struct {
	Configure                func(*sdkhost.Options)
	Run                      func(sdkhost.Options, *sdkhost.Host, []protocol.JSONObject) error
	AllowFailure             bool
	ExternalWire             *originalLoopbackWire
	WaitFirstAssistantAppend bool
}

func runNative(t *testing.T, originalInput input, supplied ...nativeControls) ([]byte, map[string]any) {
	t.Helper()
	input := originalInput
	control := nativeControls{}
	if len(supplied) > 0 {
		control = supplied[0]
	}
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
	resumeOptions := resumestate.Options{NativeProjectPaths: true, ConfigDir: filepath.Join(input.Home, ".claude"), CWD: input.Home, OriginalCWD: input.Home, CurrentSessionID: input.Temporary, NonInteractive: true, Environment: environment, Files: files, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Stat: func(path string) (resumestate.Stat, error) {
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
	firstAssistantWritten := make(chan struct{})
	var assistantNotified sync.Once
	owner := cooperative.New()
	defer owner.Close()
	bootstrap, err := bootstrapstate.New(owner, input.Temporary, input.Home, func() (string, error) { return uuid(), nil })
	must(err)
	controller, err := sessioncontroller.New(sessioncontroller.Options{Bootstrap: bootstrap, AsyncPersistence: true, RecordBranchProfile: sessioncontroller.LegacySyncRecordProfile, ResumeOptions: resumeOptions, WriterOptions: sessioncontroller.WriterOptions{ConfigDir: filepath.Join(input.Home, ".claude"), CWD: input.Home, SessionID: input.Temporary, Version: "fixture-version", Environment: environment, SessionPersistenceDisabled: input.Disabled, SettingsState: state, Files: transcriptFiles{home: input.Home, origin: input.Origin, client: client, notified: &notified, assistantWritten: func() { assistantNotified.Do(func() { close(firstAssistantWritten) }) }}, Branch: branch, OnError: onError, Schedule: sessioncontroller.RealSchedule, Now: now}, Factory: factory, UUID: uuid, Now: now, OnPhase: phase, ReplayUserMessages: true, BaseToolsCLI: []string{""}, CustomSystemPrompt: json.RawMessage(`"original custom system"`)})
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
	var controlIO sync.WaitGroup
	controlFailures := make(chan error, 1)
	hostOptions := sdkhost.Options{Controller: controller, Owner: owner, Factory: factory, UUID: uuid, Now: now, NowMillis: func() float64 { return 1700000000000 }, Global: global, Startup: startup, Settings: mustSettings(state, config, input.Home), SettingsState: state, Model: input.Model, Version: "fixture-version", CWD: input.Home, OriginalEnv: env, Runtime: input.Runtime, TLSState: tlsState, OnPhase: phase, ObserveModelInput: func(original requestretry.Input) { originalModelInputs = append(originalModelInputs, source(original)) }, ObserveRequest: func(_ string, _ http.Header, body json.RawMessage) { requests = append(requests, string(body)) }, Output: sdkhost.Output{Enqueue: func(data []byte) error {

		outputs = append(outputs, append([]byte(nil), data...))

		event := object(data)
		if event.String("type") == "system" && event.String("subtype") == "init" {
			outputTurn++
		}
		if outputTurn == 1 && event.String("type") == "stream_event" && object(event.Get("event")).String("type") == "content_block_stop" {
			controlIO.Add(1)
			go func() {
				defer controlIO.Done()
				response, e := client.Get(input.Origin + "/__fixture/release-final")
				if e != nil {
					controlFailures <- e
					return
				}
				_ = response.Body.Close()
			}()
		}
		return nil
	}}}
	if control.Configure != nil {
		control.Configure(&hostOptions)
	}
	host, e := sdkhost.New(hostOptions)
	var runError error
	if e != nil {
		runError = e
	} else if control.Run != nil {
		runError = control.Run(hostOptions, host, input.SDK)
	} else {
		for index, sdk := range input.SDK {
			// The complete-file golden chooses a serial physical-input schedule.
			// SDK result does not join the independent recordTranscript promise:
			// native stat can still be pending before it registers a tracked write.
			// Wait on this exact physical assistant append outside the Owner before
			// admitting the next input; do not alter production flush/queue policy.
			if index == 1 && control.WaitFirstAssistantAppend {
				select {
				case <-firstAssistantWritten:
				case <-time.After(10 * time.Second):
					t.Fatal("first assistant physical transcript append did not complete before next SDK input")
				}
			}
			if runError = owner.Run(context.Background(), func(scope *cooperative.Scope) error { return host.RunOne(scope, context.Background(), sdk) }); runError != nil {
				break
			}
		}
	}
	if host != nil {
		if closeError := host.Close(context.Background()); runError == nil {
			runError = closeError
		}
	}

	controlIO.Wait()
	select {
	case e := <-controlFailures:
		t.Fatalf("original fixture control I/O failed before private server cleanup: %v", e)
	default:
	}
	controller.AwaitBackground()
	history := []string{}
	must(owner.Run(context.Background(), func(*cooperative.Scope) error {
		if host == nil {
			return nil
		}
		for _, message := range host.History() {
			data, e := message.MarshalJSON()
			if e != nil {
				return e
			}
			history = append(history, string(data))
		}
		return nil
	}))
	transcripts := map[string]string{}
	must(filepath.WalkDir(filepath.Join(config, "projects"), func(path string, entry fs.DirEntry, e error) error {
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
	}))
	// Snapshot observer-only receipts under the same lock used by callbacks.
	// This does not join Record or alter the live owner/business schedule.
	mu.Lock()
	phaseSnapshot := append([]string{}, phases...)
	errorSnapshot := append([]string{}, errorRows...)
	diagnosticSnapshot := append([]sessionresume.Diagnostic{}, diagnosticRows...)
	mu.Unlock()
	result := map[string]any{"historySource": history, "transcripts": transcripts, "requests": requests, "originalModelInputs": originalModelInputs, "phases": phaseSnapshot, "cache": controller.ReadCache().Entries(), "appState": controller.AppState(), "errors": errorSnapshot, "diagnostics": diagnosticSnapshot, "runError": nil}
	if runError != nil {
		result["runError"] = map[string]any{"type": fmt.Sprintf("%T", runError), "message": runError.Error()}
	}
	if runError != nil && !control.AllowFailure {
		t.Fatalf("actual host failed: %v", runError)
	}
	joined := bytes.Join(outputs, []byte("\n"))
	if len(joined) > 0 {
		joined = append(joined, '\n')
	}
	return joined, result
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

type frozenCase struct {
	Name     string
	Input    input
	Files    map[string]string
	Wires    struct{ Prefix, FirstTail, SecondWire string }
	Expected struct {
		Stdout        string
		HistorySource []string
		Transcripts   map[string]string
		Requests      []string
		Cache         []protocol.JSONObject
		AppState      json.RawMessage
	}
}

func TestActualOriginalTwoASKReplay(t *testing.T) {
	data, e := os.ReadFile("testdata/source_contract.json")
	must(e)
	var cases []frozenCase
	must(json.Unmarshal(data, &cases))
	for _, item := range cases {
		t.Run(item.Name, func(t *testing.T) {
			home, e := filepath.EvalSymlinks(t.TempDir())
			must(e)
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			t.Setenv("BUN_OPTIONS", "--no-env-file")
			t.Setenv("CC_HAHA_FIXTURE_TEMP_ROOT", os.TempDir())
			t.Setenv("CC_HAHA_SESSION_CONTROLLER_FIXTURE", "1")
			t.Setenv("NODE_ENV", "production")
			t.Setenv("TZ", "UTC")
			t.Chdir(home)
			project := projectpath.Sanitize(home)
			replace := func(s string) string {
				return strings.ReplaceAll(strings.ReplaceAll(s, "<home>", home), "<project>", project)
			}
			for path, value := range item.Files {
				path = replace(path)
				must(os.MkdirAll(filepath.Dir(path), 0700))
				must(os.WriteFile(path, []byte(replace(value)), 0600))
				must(os.Chtimes(path, time.UnixMilli(1700000000000), time.UnixMilli(1700000000000)))
			}
			firstReady, release := make(chan struct{}), make(chan struct{})
			var onceUser, onceRelease sync.Once
			var modelMu sync.Mutex
			modelRequests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/__fixture/first-user-persisted":
					onceUser.Do(func() { close(firstReady) })
					_, _ = w.Write([]byte("OK"))
					return
				case "/__fixture/release-final":
					onceRelease.Do(func() { close(release) })
					_, _ = w.Write([]byte("OK"))
					return
				}
				if r.Method != "POST" || r.URL.RequestURI() != "/v1/messages?beta=true" {
					http.Error(w, "unexpected physical request", 400)
					return
				}
				modelMu.Lock()
				modelRequests++
				number := modelRequests
				modelMu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if number == 1 {
					w.Header().Set("Request-Id", "first-response")
					<-firstReady
					_, _ = w.Write([]byte(item.Wires.Prefix))
					w.(http.Flusher).Flush()
					if item.Name != "empty-first" {
						<-release
					}
					_, _ = w.Write([]byte(item.Wires.FirstTail))
					return
				}
				if number == 2 {
					w.Header().Set("Request-Id", "second-response")
					_, _ = w.Write([]byte(item.Wires.SecondWire))
					return
				}
				http.Error(w, "too many model requests", 400)
			}))
			defer server.Close()
			ca := []byte("-----BEGIN CERTIFICATE-----\n" + encodeCertificate(server.Certificate().Raw) + "\n-----END CERTIFICATE-----\n")
			must(os.WriteFile(filepath.Join(home, "original-ca.pem"), ca, 0600))
			item.Input.Home = home
			item.Input.Origin = server.URL
			stats := map[string]resumestate.Stat{}
			for path, value := range item.Input.Stat {
				stats[replace(path)] = value
			}
			item.Input.Stat = stats
			t.Setenv("ANTHROPIC_BASE_URL", server.URL)
			t.Setenv("ANTHROPIC_API_KEY", "offline-fixture-key")
			stdout, result := runNative(t, item.Input, nativeControls{WaitFirstAssistantAppend: item.Name != "empty-first"})
			normalize := func(s string) string {
				return strings.ReplaceAll(strings.ReplaceAll(s, project, "<project>"), home, "<home>")
			}
			if normalize(string(stdout)) != item.Expected.Stdout {
				t.Fatalf("complete SDK stdout mismatch\n%s", normalize(string(stdout)))
			}
			expected := map[string]any{"historySource": item.Expected.HistorySource, "transcripts": item.Expected.Transcripts, "requests": item.Expected.Requests, "cache": item.Expected.Cache, "appState": item.Expected.AppState}
			for name, value := range expected {
				if normalize(source(result[name])) != source(value) {
					t.Fatalf("actual %s mismatch\n%s\n%s", name, normalize(source(result[name])), source(value))
				}
			}
			if modelRequests != 2 {
				t.Fatalf("two actual model requests required, got %d", modelRequests)
			}
		})
	}
}
func encodeCertificate(data []byte) string { return base64.StdEncoding.EncodeToString(data) }
