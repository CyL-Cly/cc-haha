package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type privateFiles struct {
	home       string
	held       <-chan struct{}
	hold       bool
	probing    *atomic.Bool
	probeReads *atomic.Int64
	probePath  string
	liteFault  *liteFault
	liteFaults *[]map[string]any
}

func (files privateFiles) checked(path string) { must(guard(files.home, path, false)) }
func (files privateFiles) Open(path string) (sessionstore.File, error) {
	files.checked(path)
	value, err := (resumestate.OSFiles{}).Open(path)
	if err == nil && files.liteFault != nil && path == files.liteFault.Path {
		return &liteFaultFile{File: value, fault: files.liteFault, records: files.liteFaults}, nil
	}
	return value, err
}

type liteFault struct {
	Path   string `json:"path"`
	AtRead int    `json:"atRead"`
}
type liteFaultFile struct {
	sessionstore.File
	fault   *liteFault
	calls   int
	records *[]map[string]any
}

func (f *liteFaultFile) ReadAt(data []byte, position int64) (int, error) {
	f.calls++
	if f.calls == f.fault.AtRead {
		*f.records = append(*f.records, map[string]any{"operation": "read", "ordinal": f.calls, "position": position})
		return 0, errors.New("original-private-tail-read-fault")
	}
	return f.File.ReadAt(data, position)
}
func (files privateFiles) ReadFile(path string) ([]byte, error) {
	files.checked(path)
	if files.probing.Load() && path == files.probePath {
		files.probeReads.Add(1)
	}
	return os.ReadFile(path)
}
func (files privateFiles) WriteFile(path string, value []byte) error {
	files.checked(path)
	return (resumestate.OSFiles{}).WriteFile(path, value)
}
func (files privateFiles) MkdirAll(path string) error {
	files.checked(path)
	return (resumestate.OSFiles{}).MkdirAll(path)
}
func (files privateFiles) ReadDir(path string) ([]fs.DirEntry, error) {
	files.checked(path)
	return os.ReadDir(path)
}
func (files privateFiles) Link(old, new string) error {
	files.checked(old)
	files.checked(new)
	if files.hold {
		<-files.held
	}
	return os.Link(old, new)
}
func (files privateFiles) CopyFile(old, new string) error {
	files.checked(old)
	files.checked(new)
	return (resumestate.OSFiles{}).CopyFile(old, new)
}

type input struct {
	Home             string                      `json:"home"`
	CurrentSessionID string                      `json:"currentSessionId"`
	Source           *string                     `json:"source"`
	JSONLPath        string                      `json:"jsonlPath"`
	Bare             bool                        `json:"bare"`
	Checkpointing    bool                        `json:"checkpointing"`
	EnvironmentKind  string                      `json:"environmentKind"`
	PolicyPath       string                      `json:"policyPath"`
	Stat             map[string]resumestate.Stat `json:"stat"`
	HoldCopies       bool                        `json:"holdCopies"`
	Request          *requestInput               `json:"request"`
	UUIDProbes       []string                    `json:"uuidProbes"`
	LiteReadFault    *liteFault                  `json:"liteReadFault"`
	LiteHelper       *helperInput                `json:"liteHelper"`
}

func source(value any) string {
	raw, err := json.Marshal(value)
	must(err)
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	must(err)
	return string(raw)
}
func failure(err error) map[string]any {
	var typed *sessionresume.Failure
	if errors.As(err, &typed) {
		return map[string]any{"name": typed.Name, "message": typed.MessageValue()}
	}
	message := err.Error()
	var native *os.PathError
	if errors.As(err, &native) {
		message = strings.TrimPrefix(storage.JavaScriptErrorText(err), "Error: ")
	}
	return map[string]any{"name": "Error", "message": message}
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
	must(guard(input.Home, input.PolicyPath, true))
	output := filepath.Join(input.Home, "resume-state-result.json")
	must(guard(input.Home, output, true))
	if input.JSONLPath != "" {
		must(guard(input.Home, input.JSONLPath, true))
	}
	for path := range input.Stat {
		must(guard(input.Home, path, true))
	}
	if input.LiteReadFault != nil {
		fault := input.LiteReadFault
		must(guard(input.Home, fault.Path, true))
		stat, exists := input.Stat[fault.Path]
		if input.Source != nil || input.JSONLPath != "" || fault.AtRead != 2 || !exists || stat.Size <= 65536 {
			panic("Lite read fault requires exact original private continue path")
		}
	}
	if os.Getenv("CC_HAHA_RESUME_STATE_FIXTURE") != "1" || os.Getenv("NODE_ENV") != "production" || input.Stat == nil {
		panic("Resume state requires original offline inputs")
	}
	if input.Request != nil {
		must(validateRequest(input.Request))
		must(guard(input.Home, filepath.Join(input.Home, ".claude", ".claude.json"), true))
	}
	if input.LiteHelper != nil {
		executeLiteHelper(input)
		return
	}
	held := make(chan struct{})
	var probeSID string
	if input.UUIDProbes != nil {
		if input.Source == nil || input.JSONLPath != "" {
			panic("UUID probes require original SID selector")
		}
		must(json.Unmarshal([]byte(*input.Source), &probeSID))
	}
	liteFaults := []map[string]any{}
	files := privateFiles{home: input.Home, held: held, hold: input.HoldCopies, probing: &atomic.Bool{}, probeReads: &atomic.Int64{}, probePath: filepath.Join(input.Home, ".claude", "projects", sanitized(input.Home), probeSID+".jsonl"), liteFault: input.LiteReadFault, liteFaults: &liteFaults}
	var mu sync.Mutex
	allocations, diagnostics, errorRows, rawErrors := []map[string]any{}, []map[string]any{}, []map[string]any{}, []map[string]any{}
	phases := []string{}
	calls := 0
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		allocations = append(allocations, map[string]any{"kind": "time", "value": "2023-11-14T22:13:20.000Z"})
		return time.UnixMilli(1700000000000)
	}
	nowMillis := func() int64 {
		mu.Lock()
		defer mu.Unlock()
		allocations = append(allocations, map[string]any{"kind": "now", "value": int64(1700000000000)})
		return 1700000000000
	}
	uuid := func() string {
		mu.Lock()
		defer mu.Unlock()
		calls++
		value := fmt.Sprintf("00000000-0000-4000-8000-%012d", calls)
		allocations = append(allocations, map[string]any{"kind": "uuid", "value": value})
		return value
	}
	onError := func(err error) {
		now()
		mu.Lock()
		defer mu.Unlock()
		row := failure(err)
		errorRows = append(errorRows, row)
		copy := failure(err)
		copy["stack"] = string(debug.Stack())
		rawErrors = append(rawErrors, copy)
	}
	onDiagnostic := func(event sessionresume.Diagnostic) {
		mu.Lock()
		defer mu.Unlock()
		row := map[string]any{"kind": event.Kind, "name": event.Name}
		if event.Fields != nil {
			row["fields"] = event.Fields
		}
		diagnostics = append(diagnostics, row)
	}
	factory, err := messages.NewFactory(uuid, now)
	must(err)
	processCwd, err := os.Getwd()
	must(err)
	reducer, err := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(path json.RawMessage) (json.RawMessage, error) {
		return sessionresume.RelativePath(input.Home, processCwd, path)
	}, OnDiagnostic: onDiagnostic, OnError: onError})
	must(err)
	loader, err := sessionhistory.New(sessionhistory.Options{Files: files, Stat: func(path string) (int64, error) {
		files.checked(path)
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}, ReadFile: files.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return true }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(event sessionhistory.Diagnostic) {
		if event.Message != "" {
			onDiagnostic(sessionresume.Diagnostic{Kind: "debug", Name: event.Message})
		}
		if event.Code != "" {
			onDiagnostic(sessionresume.Diagnostic{Kind: "event", Name: event.Code, Fields: event.Fields})
		}
	}})
	must(err)
	environment := map[string]string{"CLAUDE_CODE_SIMPLE": fmt.Sprint(input.Bare), "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING": fmt.Sprint(input.Checkpointing)}
	settingsState, err := clisettings.NewState(func() float64 { return float64(nowMillis()) })
	must(err)
	service, err := resumestate.New(resumestate.Options{ConfigDir: filepath.Join(input.Home, ".claude"), CWD: input.Home, OriginalCWD: input.Home, CurrentSessionID: input.CurrentSessionID, EnvironmentKind: input.EnvironmentKind, Environment: environment, Arguments: []string{}, NonInteractive: true, Files: files, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Stat: func(path string) (resumestate.Stat, error) {
		files.checked(path)
		if _, err := os.Stat(path); err != nil {
			return resumestate.Stat{}, err
		}
		value, exists := input.Stat[path]
		if !exists {
			return resumestate.Stat{}, errors.New("stat seam requires original filesystem metadata")
		}
		return value, nil
	}, Now: nowMillis, UUID: uuid, OnDiagnostic: onDiagnostic, OnError: onError, OnPhase: func(value string) { phases = append(phases, value) }, PolicyPaths: []string{input.PolicyPath}, SettingsState: settingsState})
	must(err)
	var original json.RawMessage
	if input.Source != nil {
		original = json.RawMessage(*input.Source)
	}
	result, err := service.Load(resumestate.Input{Source: original, JSONLPath: input.JSONLPath})
	var failureValue any
	if err != nil {
		failureValue = failure(err)
		phases = append(phases, "throw")
	} else {
		phases = append(phases, "return")
	}
	state := func() map[string]any {
		value := service.State()
		return map[string]any{"skills": value.Skills, "planSlugs": value.PlanSlugs, "suppressed": value.SuppressNextSkillListing, "pendingEntries": value.PendingEntries}
	}
	before := state()
	close(held)
	service.AwaitBackground()
	after := state()
	effects := map[string]string{}
	for _, directory := range []string{filepath.Join(input.Home, ".claude", "plans"), filepath.Join(input.Home, "private-plans"), filepath.Join(input.Home, ".claude", "file-history", input.CurrentSessionID)} {
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			files.checked(path)
			if entry.Type().IsRegular() {
				data, err := files.ReadFile(path)
				if err != nil {
					return err
				}
				part, err := filepath.Rel(input.Home, path)
				if err != nil {
					return err
				}
				effects[part] = hex.EncodeToString(data)
			}
			return nil
		})
		must(err)
	}
	var resultSource any
	if err == nil {
		resultSource = source(result)
	}
	observed := map[string]any{"resultSource": resultSource, "before": before, "after": after, "phases": phases, "effects": effects, "allocations": allocations, "diagnostics": diagnostics, "errors": errorRows, "failure": failureValue}
	if input.UUIDProbes != nil {
		rows := []map[string]any{}
		files.probing.Store(true)
		for _, probe := range input.UUIDProbes {
			exists := false
			for _, raw := range service.State().SessionUUIDs[probeSID] {
				var value string
				if json.Unmarshal(raw, &value) == nil && value == probe {
					exists = true
				}
			}
			rows = append(rows, map[string]any{"uuid": probe, "exists": exists})
		}
		files.probing.Store(false)
		observed["uuidProbes"] = rows
		observed["transcriptReadsDuringProbes"] = files.probeReads.Load()
	}
	// Snapshot the return/background contract before the next query mutates its
	// own state or allocates provider IDs.
	outer := map[string]any{"observation": json.RawMessage(source(observed)), "sourceOnly": map[string]any{"rawErrors": rawErrors}}
	if input.LiteReadFault != nil {
		outer["sourceOnly"].(map[string]any)["liteReadFaults"] = liteFaults
	}
	if input.Request != nil {
		request, originalAllocations, err := executeRequest(input.Request, result, reducer, uuid, now, &allocations)
		if err != nil {
			outer["requestFailure"] = failure(err)
		} else {
			outer["requestObservation"] = request
		}
		outer["sourceOnly"].(map[string]any)["request"] = originalAllocations
	}
	must(os.WriteFile(output, []byte(source(outer)), 0600))
	fmt.Println(`{"result":"resume-state-result.json"}`)
}
