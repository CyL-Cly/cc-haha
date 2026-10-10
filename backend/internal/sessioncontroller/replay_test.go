package sessioncontroller

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

type replayInput struct {
	Home, Temporary, SID, ResumeAt, Model                      string
	Continue, ForkSession, Checkpointing, Disabled, HoldCopies bool
	ResumeInterrupted                                          string
	SDK                                                        []protocol.JSONObject
}
type replayCase struct {
	Name     string
	Input    replayInput
	Files    map[string]string
	Expected json.RawMessage
}

func writePrivate(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	fixed := time.UnixMilli(1700000000000)
	if err := os.Chtimes(path, fixed, fixed); err != nil {
		t.Fatal(err)
	}
}
func marshal(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = protocol.ParseAndStringifyJavaScriptJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
func rebase(value, home string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, sanitize("/__fixture_home__"), sanitize(home)), "/__fixture_home__", home)
}

type testHost struct {
	options Options
	phase   *[]string
	errors  *[]string
}

func hostOptions(t *testing.T, home string) testHost {
	t.Helper()
	var mu sync.Mutex
	phases, errorsList := []string{}, []string{}
	scope, counter := "startup", 0
	now := func() time.Time { return time.UnixMilli(1700000000000).UTC() }
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
	phase := func(value string) {
		mu.Lock()
		defer mu.Unlock()
		phases = append(phases, value)
		if value == "resume:start" {
			scope = "resume"
			counter = 0
		}
		if value == "ask:start" {
			scope = "turn"
			counter = 0
		}
	}
	onError := func(err error) { mu.Lock(); defer mu.Unlock(); errorsList = append(errorsList, err.Error()) }
	factory, err := messages.NewFactory(uuid, now)
	if err != nil {
		t.Fatal(err)
	}
	files := resumestate.OSFiles{}
	reducer, err := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(value json.RawMessage) (json.RawMessage, error) {
		return sessionresume.RelativePath(home, home, value)
	}, OnDiagnostic: func(sessionresume.Diagnostic) {}, OnError: onError})
	if err != nil {
		t.Fatal(err)
	}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: files, Stat: func(path string) (int64, error) {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}, ReadFile: files.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return true }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := clisettings.NewState(func() float64 { return 1700000000000 })
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "NODE_ENV": "production", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING": "false"}
	options := Options{ResumeOptions: resumestate.Options{ConfigDir: filepath.Join(home, ".claude"), CWD: home, OriginalCWD: home, CurrentSessionID: "22222222-2222-4222-8222-222222222222", NonInteractive: true, Environment: env, Files: files, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Stat: func(path string) (resumestate.Stat, error) {
		info, err := os.Stat(path)
		if err != nil {
			return resumestate.Stat{}, err
		}
		return resumestate.Stat{Modified: float64(info.ModTime().UnixMilli()), Birth: 0, Size: info.Size()}, nil
	}, Now: func() int64 { return now().UnixMilli() }, UUID: uuid, OnDiagnostic: func(sessionresume.Diagnostic) {}, OnError: onError, OnPhase: func(string) {}, PolicyPaths: []string{filepath.Join(home, "policy", "managed-settings.json")}, SettingsState: state}, WriterOptions: WriterOptions{ConfigDir: filepath.Join(home, ".claude"), CWD: home, SessionID: "22222222-2222-4222-8222-222222222222", Version: "fixture-version", Environment: env, SettingsState: state, Files: OSFiles{}, Branch: func() (string, error) {
		return NoRepositoryBranch(home, func(path string) (os.FileInfo, error) {
			if path != filepath.Join(home, ".git") {
				return nil, os.ErrNotExist
			}
			return os.Stat(path)
		})
	}, OnError: onError, Schedule: func(time.Duration, func()) func() { return func() {} }, Now: now}, Factory: factory, UUID: uuid, Now: now, OnPhase: phase, ReplayUserMessages: true, BaseToolsCLI: []string{""}, CustomSystemPrompt: quote("original custom system")}
	return testHost{options, &phases, &errorsList}
}
func directoryBytes(t *testing.T, home, area string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(filepath.Join(home, ".claude", area), func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(home, path)
			result[relative] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func replay(t *testing.T, row replayCase, home string) map[string]any {
	t.Helper()
	for path, value := range row.Files {
		writePrivate(t, path, value)
	}
	host := hostOptions(t, home)
	options := host.options
	options.ResumeOptions.CurrentSessionID = row.Input.Temporary
	options.WriterOptions.SessionID = row.Input.Temporary
	options.WriterOptions.SessionPersistenceDisabled = row.Input.Disabled
	options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = fmt.Sprint(row.Input.Checkpointing)
	options.ResumeOptions.Environment["CLAUDE_CODE_RESUME_INTERRUPTED_TURN"] = row.Input.ResumeInterrupted
	if row.Input.HoldCopies {
		options.ResumeOptions.Files = &replayHeldFiles{OSFiles: resumestate.OSFiles{}, release: make(chan struct{})}
		prior := options.OnPhase
		options.OnPhase = func(value string) {
			prior(value)
			if value == "read-cache:return" {
				close(options.ResumeOptions.Files.(*replayHeldFiles).release)
			}
		}
	}
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.AwaitBackground()
	var selection json.RawMessage
	if !row.Input.Continue {
		selection = quote(row.Input.SID)
	}
	if err = controller.Activate(Activation{Resume: resumestate.Input{Source: selection}, ForkSession: row.Input.ForkSession, ResumeAt: row.Input.ResumeAt}); err != nil {
		t.Fatal(err)
	}
	background := map[string]bool{}
	if row.Input.HoldCopies {
		background["returnedBeforeCopy"] = controller.ResumeReturnedSnapshotCount() == 0
	}
	history, appState, cache := controller.History(), controller.AppState(), controller.ReadCache().Entries()
	checks := []map[string]any{}
	admissions, err := controller.AdmitOriginalBatch(row.Input.SDK)
	if err != nil {
		t.Fatal(err)
	}
	for index, input := range row.Input.SDK {
		if truthy(input.Get("uuid")) {
			checks = append(checks, map[string]any{"uuid": input.Get("uuid"), "exists": admissions[index].Historical})
		}
	}

	accepted, err := controller.AcceptNext()
	if err != nil {
		t.Fatal(err)
	}
	requests := []string{}
	if accepted != nil {
		prepared, err := controller.Prepare(requestcontext.Context{Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: row.Input.Model}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: map[string]string{"ANTHROPIC_API_KEY": "offline-fixture-key", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, DeviceID: "fixture-controller-device", SDKBetas: []string{}, TTLAllowlist: []string{}}}, requestcontext.Dependencies{UUID: options.UUID, Now: options.Now})
		if err != nil {
			t.Fatal(err)
		}
		request, err := prepared.BuildAttempt(requestcontext.AttemptOptions{Model: row.Input.Model})
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, string(request.PreparedRequest.Body))
		options.OnPhase("HTTP")
	}
	controller.AwaitBackground()
	if err = controller.Writer().Flush(); err != nil {
		t.Fatal(err)
	}
	if err = controller.Writer().RefreshMetadata(); err != nil {
		t.Fatal(err)
	}
	historySource := []string{}
	for _, message := range controller.History() {
		historySource = append(historySource, marshal(t, message))
	}
	return map[string]any{"activationHistory": history, "snapshotRecords": controller.SnapshotRecords(), "background": background, "appState": appState, "cache": cache, "uuidChecks": checks, "enqueued": controller.QueueEvents(), "replays": controller.Replays(), "boundaryOrder": selectedPhases(*host.phase), "historySource": historySource, "requests": requests, "transcripts": directoryBytes(t, home, "projects"), "copies": directoryBytes(t, home, "file-history"), "errors": *host.errors}
}
func TestActualHeadlessSourceReplay(t *testing.T) {
	data, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []json.RawMessage
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	for _, raw := range rows {
		var preview replayCase
		if err = json.Unmarshal(raw, &preview); err != nil {
			t.Fatal(err)
		}
		t.Run(preview.Name, func(t *testing.T) {
			home := t.TempDir()
			var row replayCase
			if err := json.Unmarshal([]byte(rebase(string(raw), home)), &row); err != nil {
				t.Fatal(err)
			}
			observed := replay(t, row, home)
			var expected, actual any
			if err := json.Unmarshal(row.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(marshal(t, observed)), &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("actual Source host replay diverged\nactual %s\nexpected %s", marshal(t, observed), row.Expected)
			}
		})
	}
}

func selectedPhases(phases []string) []string {
	result := []string{}
	for _, value := range phases {
		switch value {
		case "resume:start", "resume:return", "switch", "reset", "state:restore", "metadata:restore", "enqueue", "dequeue", "lifecycle:started", "lifecycle:completed", "ask:start", "record", "HTTP":
			result = append(result, value)
		}
	}
	return result
}

type replayHeldFiles struct {
	resumestate.OSFiles
	release chan struct{}
}

func (files *replayHeldFiles) Link(old, new string) error {
	<-files.release
	return files.OSFiles.Link(old, new)
}
