package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type initialMultiQueue struct {
	Target string   `json:"target"`
	Labels []string `json:"labels"`
}
type initialMultiOutcome struct {
	Status string `json:"status"`
	Code   string `json:"code"`
}
type initialMultiState struct {
	Writes       []string            `json:"writes"`
	Files        map[string][]string `json:"files"`
	Kinds        map[string]string   `json:"kinds"`
	Queues       []initialMultiQueue `json:"queues"`
	Tracked      int                 `json:"tracked"`
	TimerPending bool                `json:"timerPending"`
	Active       bool                `json:"active"`
}
type initialMultiObservation struct {
	Outcomes []initialMultiOutcome `json:"outcomes"`
	initialMultiState
}
type initialMultiAdmission struct {
	InitialTargets int                 `json:"initialTargets"`
	Pending        []bool              `json:"pending"`
	Distinct       bool                `json:"distinct"`
	Queues         []initialMultiQueue `json:"queues"`
	TimerPending   bool                `json:"timerPending"`
	Active         bool                `json:"active"`
}
type initialMultiSource struct {
	Profile      string                  `json:"profile"`
	SourceSHA256 string                  `json:"sourceSHA256"`
	Admission    initialMultiAdmission   `json:"admission"`
	Observation  initialMultiObservation `json:"observation"`
	Final        initialMultiState       `json:"final"`
	RawErrors    []string                `json:"rawErrors"`
	Blocked      []string                `json:"blocked"`
	Physical     bool                    `json:"physical"`
}

// Observe immutable append bytes only. Every real OS operation/result is
// retained; no readiness gate, replacement descriptor or scheduled callback.
type initialMultiFiles struct {
	OSFiles
	mu     sync.Mutex
	bodies []string
}
type initialMultiFile struct {
	TranscriptFile
	files *initialMultiFiles
}

func (files *initialMultiFiles) OpenFile(path string, flag int, mode fs.FileMode) (TranscriptFile, error) {
	file, err := files.OSFiles.OpenFile(path, flag, mode)
	if err != nil {
		return nil, err
	}
	return &initialMultiFile{TranscriptFile: file, files: files}, nil
}
func (file *initialMultiFile) Write(data []byte) (int, error) {
	file.files.mu.Lock()
	file.files.bodies = append(file.files.bodies, string(data))
	file.files.mu.Unlock()
	return file.TranscriptFile.Write(data)
}
func initialMultiLabels(text string) ([]string, error) {
	labels := []string{}
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		var entry struct {
			Label string `json:"customTitle"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, err
		}
		labels = append(labels, entry.Label)
	}
	return labels, nil
}
func initialMultiQueues(writer *Writer, paths map[string]string) []initialMultiQueue {
	rows := []initialMultiQueue{}
	for generation := writer.queueFirst; generation != nil; generation = generation.next {
		if writer.queueGenerations[generation.path] != generation {
			continue
		}
		label := ""
		for target, path := range paths {
			if path == generation.path {
				label = target
			}
		}
		values := []string{}
		for _, queued := range writer.queues[generation.path] {
			values = append(values, queued.entry.String("customTitle"))
		}
		rows = append(rows, initialMultiQueue{Target: label, Labels: values})
	}
	return rows
}
func initialMultiSnapshot(writer *Writer, pending *PendingWriter, files *initialMultiFiles, paths map[string]string) (initialMultiState, error) {
	result := initialMultiState{Writes: []string{}, Files: map[string][]string{}, Kinds: map[string]string{}}
	files.mu.Lock()
	bodies := append([]string(nil), files.bodies...)
	files.mu.Unlock()
	for _, body := range bodies {
		labels, err := initialMultiLabels(body)
		if err != nil {
			return result, err
		}
		result.Writes = append(result.Writes, labels...)
	}
	for _, target := range []string{"A", "B", "C"} {
		info, err := os.Stat(paths[target])
		if err != nil {
			return result, err
		}
		result.Files[target] = []string{}
		result.Kinds[target] = "directory"
		if info.Mode().IsRegular() {
			result.Kinds[target] = "file"
			data, err := os.ReadFile(paths[target])
			if err != nil {
				return result, err
			}
			result.Files[target], err = initialMultiLabels(string(data))
			if err != nil {
				return result, err
			}
		}
	}
	writer.mu.Lock()
	result.Queues = initialMultiQueues(writer, paths)
	result.Tracked, result.TimerPending, result.Active = pending.tracked, writer.stop != nil, pending.active != nil
	writer.mu.Unlock()
	return result, nil
}
func initialMultiEnqueue(writer *Writer, path, label string) error {
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("custom-title")}, protocol.JSONField{Name: "customTitle", Value: quote(label)}, protocol.JSONField{Name: "sessionId", Value: quote(writer.currentSessionID())})
	writer.mu.Lock()
	defer writer.mu.Unlock()
	previous := writer.path
	writer.path = path
	defer func() { writer.path = previous }()
	return writer.enqueue(&entry)
}
func initialMultiOutcomeFor(err error) initialMultiOutcome {
	if err == nil {
		return initialMultiOutcome{Status: "fulfilled"}
	}
	code := "UNKNOWN"
	if errors.Is(err, syscall.EISDIR) {
		code = "EISDIR"
	}
	var unsupported *CapabilityError
	if errors.As(err, &unsupported) {
		code = "CAPABILITY"
	}
	return initialMultiOutcome{Status: "rejected", Code: code}
}
func initialMultiExpected(profile string) (initialMultiAdmission, initialMultiObservation, initialMultiState) {
	row := func(target string, labels ...string) initialMultiQueue {
		return initialMultiQueue{Target: target, Labels: append([]string{}, labels...)}
	}
	admission := initialMultiAdmission{InitialTargets: 3, Pending: []bool{true}, Distinct: true, Queues: []initialMultiQueue{row("A"), row("B", "B"), row("C", "C")}}
	state := initialMultiState{Writes: []string{"A", "B", "C"}, Files: map[string][]string{"A": {"A"}, "B": {"B"}, "C": {"C"}}, Kinds: map[string]string{"A": "file", "B": "file", "C": "file"}, Queues: []initialMultiQueue{}}
	observation := initialMultiObservation{Outcomes: []initialMultiOutcome{{Status: "fulfilled"}}, initialMultiState: state}
	final := state
	if profile == "delete-reinsert" {
		admission.Queues = []initialMultiQueue{row("A"), row("C", "C"), row("B", "B2")}
		admission.TimerPending = true
		state.Writes = []string{"A", "C", "B2"}
		state.Files = map[string][]string{"A": {"A"}, "B": {"B2"}, "C": {"C"}}
		observation.initialMultiState, final = state, state
	}
	if profile == "error-interrupt" {
		state.Writes = []string{"A"}
		state.Files = map[string][]string{"A": {"A"}, "B": {}, "C": {}}
		state.Kinds = map[string]string{"A": "file", "B": "directory", "C": "file"}
		state.Queues = []initialMultiQueue{row("A"), row("B"), row("C", "C")}
		observation = initialMultiObservation{Outcomes: []initialMultiOutcome{{Status: "rejected", Code: "EISDIR"}}, initialMultiState: state}
		final = initialMultiState{Writes: []string{"A", "C"}, Files: map[string][]string{"A": {"A"}, "B": {}, "C": {"C"}}, Kinds: map[string]string{"A": "file", "B": "file", "C": "file"}, Queues: []initialMultiQueue{}}
	}
	if profile == "independent-drains" {
		admission.Pending = []bool{true, true}
		admission.Queues = []initialMultiQueue{row("A"), row("B"), row("C", "C")}
		state.Writes = []string{"B", "C"}
		state.Files = map[string][]string{"A": {}, "B": {"B"}, "C": {"C"}}
		state.Kinds = map[string]string{"A": "directory", "B": "file", "C": "file"}
		observation = initialMultiObservation{Outcomes: []initialMultiOutcome{{Status: "rejected", Code: "EISDIR"}, {Status: "fulfilled"}}, initialMultiState: state}
		final = initialMultiState{Writes: []string{"B", "C"}, Files: map[string][]string{"A": {}, "B": {"B"}, "C": {"C"}}, Kinds: map[string]string{"A": "file", "B": "file", "C": "file"}, Queues: []initialMultiQueue{}}
	}
	return admission, observation, final
}

func actualSourceInitialMultiTargetDrain(t *testing.T, profile string) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sourceHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-initial-multi-target-drain.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = sourceHome
	command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_INITIAL_MULTI_TARGET=1", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Join(sourceHome, "bin")}
	input, _ := json.Marshal(map[string]string{"home": sourceHome, "profile": profile})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	raw, sourceErr := command.Output()
	if sourceErr != nil {
		t.Fatalf("actual Source multi-target setup: %v raw=%s stderr=%s", sourceErr, raw, stderr.String())
	}
	var source initialMultiSource
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatalf("actual Source JSON: %v raw=%s", err, raw)
	}
	wantAdmission, wantObservation, wantFinal := initialMultiExpected(profile)
	if stderr.Len() != 0 || source.Profile != profile || source.SourceSHA256 != "bc05983c8ed2164c437f3bf3cb11592a0447ca4ae94605bfa4bff0158f01dac6" || !source.Physical || len(source.Blocked) != 0 || !reflect.DeepEqual(source.Admission, wantAdmission) || !reflect.DeepEqual(source.Observation, wantObservation) || !reflect.DeepEqual(source.Final, wantFinal) {
		t.Fatalf("invalid actual original Source multi-target proof: %s stderr=%s", raw, stderr.String())
	}
	t.Logf("actual original Source multi-target physical receipt %s", raw)
	owner := cooperative.New()
	defer owner.Close()
	files := &initialMultiFiles{}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	writer := actualPendingWriter(t, owner, home, files)
	paths := map[string]string{}
	for _, target := range []string{"A", "B", "C"} {
		paths[target] = filepath.Join(home, "targets", target+".jsonl")
	}
	var pending *PendingWriter
	problem := owner.Run(ctx, func(scope *cooperative.Scope) error {
		writer.options.Schedule = func(time.Duration, func()) func() { return func() {} }
		var err error
		pending, err = NewPendingWriter(writer, owner)
		if err != nil {
			return err
		}
		for _, target := range []string{"A", "B", "C"} {
			writer.mu.Lock()
			err = writer.prepare(paths[target])
			writer.mu.Unlock()
			if err != nil {
				return err
			}
			if err = initialMultiEnqueue(writer, paths[target], target); err != nil {
				return err
			}
		}
		return nil
	})
	if problem != nil {
		if pending != nil {
			_ = pending.Close(context.Background())
		}
		owner.Close()
		t.Fatal(problem)
	}
	damaged, saved := "", ""
	if profile == "error-interrupt" || profile == "independent-drains" {
		damaged = paths["B"]
		if profile == "independent-drains" {
			damaged = paths["A"]
		}
		saved = damaged + ".saved"
		if err = os.Rename(damaged, saved); err == nil {
			err = os.Mkdir(damaged, 0700)
		}
		if err != nil {
			_ = pending.Close(context.Background())
			owner.Close()
			t.Fatal(err)
		}
	}
	futures := []*FlushFuture{}
	admission := initialMultiAdmission{Pending: []bool{}, Distinct: true}
	problem = owner.Run(ctx, func(scope *cooperative.Scope) error {
		writer.mu.Lock()
		admission.InitialTargets = len(writer.queues)
		writer.mu.Unlock()
		first, err := pending.Flush(scope)
		if err != nil {
			return err
		}
		futures = append(futures, first)
		if profile == "delete-reinsert" {
			writer.mu.Lock()
			writer.deleteQueueLocked(paths["B"])
			writer.mu.Unlock()
			if err = initialMultiEnqueue(writer, paths["B"], "B2"); err != nil {
				return err
			}
		}
		if profile == "independent-drains" {
			second, err := pending.Flush(scope)
			if err != nil {
				return err
			}
			futures = append(futures, second)
			admission.Distinct = first != second
		}
		for _, future := range futures {
			select {
			case <-future.Done():
				admission.Pending = append(admission.Pending, false)
			default:
				admission.Pending = append(admission.Pending, true)
			}
		}
		writer.mu.Lock()
		admission.Queues = initialMultiQueues(writer, paths)
		admission.TimerPending = writer.stop != nil
		admission.Active = pending.active != nil
		writer.mu.Unlock()
		return nil
	})
	outcomes := []initialMultiOutcome{}
	errorsSeen := []error{}
	for _, future := range futures {
		failure := future.Wait(context.Background())
		errorsSeen = append(errorsSeen, failure)
		outcomes = append(outcomes, initialMultiOutcomeFor(failure))
	}
	var observed initialMultiState
	if problem == nil {
		problem = owner.Run(ctx, func(*cooperative.Scope) error {
			var err error
			observed, err = initialMultiSnapshot(writer, pending, files, paths)
			return err
		})
	}
	// Restore the real damaged path, then final flush and physical Close.
	// No expected business assertion may precede all worker/descriptor joins.
	if damaged != "" {
		if err = os.RemoveAll(damaged); err == nil {
			err = os.Rename(saved, damaged)
		}
		if problem == nil {
			problem = err
		}
	}
	var finalFlush *FlushFuture
	joinErr := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		finalFlush, err = pending.Flush(scope)
		return err
	})
	if joinErr == nil {
		joinErr = finalFlush.Wait(context.Background())
	}
	closeErr := pending.Close(context.Background())
	owner.Close()
	final, finalErr := initialMultiSnapshot(writer, pending, files, paths)
	goReceipt := struct {
		Admission   initialMultiAdmission   `json:"admission"`
		Observation initialMultiObservation `json:"observation"`
		Final       initialMultiState       `json:"final"`
		Errors      []string                `json:"errors"`
	}{Admission: admission, Observation: initialMultiObservation{Outcomes: outcomes, initialMultiState: observed}, Final: final, Errors: []string{}}
	for _, failure := range append(errorsSeen, joinErr, closeErr) {
		if failure != nil {
			goReceipt.Errors = append(goReceipt.Errors, failure.Error())
		}
	}
	goRaw, _ := json.Marshal(goReceipt)
	t.Logf("actual Go initial multi-target physical receipt %s", goRaw)
	for _, failure := range append(errorsSeen, joinErr, closeErr) {
		var unsupported *CapabilityError
		if errors.As(failure, &unsupported) && unsupported.Boundary == "independent multi-target transcript drain order" {
			t.Fatalf("actual Source multi-target flush is still rejected by obsolete Go capability after physical Close: %v SourceRaw=%s GoRaw=%s", failure, raw, goRaw)
		}
	}
	if problem != nil || joinErr != nil || closeErr != nil || finalErr != nil {
		t.Fatalf("multi-target physical cleanup: observation=%v finalFlush=%v Close=%v disk=%v", problem, joinErr, closeErr, finalErr)
	}
	if !reflect.DeepEqual(goReceipt.Admission, source.Admission) || !reflect.DeepEqual(goReceipt.Observation, source.Observation) || !reflect.DeepEqual(goReceipt.Final, source.Final) {
		t.Fatal(fmt.Sprintf("initial multi-target Map/flush behavior differs from actual original Source: SourceRaw=%s GoRaw=%s", raw, goRaw))
	}
}

func TestActualSourceInitialMultiTargetDrainInsertionOrder(t *testing.T) {
	actualSourceInitialMultiTargetDrain(t, "initial")
}

func TestActualSourceInitialMultiTargetDrainDeleteReinsertMovesTail(t *testing.T) {
	actualSourceInitialMultiTargetDrain(t, "delete-reinsert")
}

func TestActualSourceInitialMultiTargetDrainErrorStopsLaterTargets(t *testing.T) {
	actualSourceInitialMultiTargetDrain(t, "error-interrupt")
}

func TestActualSourceInitialMultiTargetDrainIndependentFlushErrors(t *testing.T) {
	actualSourceInitialMultiTargetDrain(t, "independent-drains")
}
