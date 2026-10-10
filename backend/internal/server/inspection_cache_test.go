package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func inspectionCacheServer(t *testing.T) (*Server, sessionInfo) {
	t.Helper()
	s, info, _, _ := recoveryLifecycleServer(t)
	empty := func() (*protocol.JSONObject, error) { value := protocol.NewJSONObject(); return &value, nil }
	s.options.InspectionModelOptions = &modelaccounting.NativeCalculatorOptions{Environment: map[string]string{"ANTHROPIC_API_KEY": "fake-no-network", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210"}, Settings: empty, Global: empty}
	return s, info
}

func TestInspectionSharedSnapshotSurvivesCallerCancellationAndJoinsRealReader(t *testing.T) {
	s, info := inspectionCacheServer(t)
	faults := &recoveryFaultFiles{entered: make(chan struct{}), release: make(chan struct{})}
	s.reader = sessionstore.NewReaderWithBudget(faults, s.budget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	errors := make(chan error, 9)
	wg.Add(1)
	go func() { defer wg.Done(); _, err := s.getInspectionTranscript(ctx, info); errors <- err }()
	select {
	case <-faults.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Actual snapshot did not enter semantic reader")
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.getInspectionTranscript(context.Background(), info)
			errors <- err
		}()
	}
	cancel()
	close(faults.release)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal("Original shared snapshot was cancelled by one caller", err)
		}
	}
	if faults.closed.Load() != 1 || len(s.inspectionSnapshots) != 1 {
		t.Fatal("Concurrent snapshot did not coalesce actual reader", faults.closed.Load(), len(s.inspectionSnapshots))
	}
	value, err := s.getInspectionTranscript(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	value.LaunchInfo.Set("workDir", jsonString("corrupted caller copy"))
	again, err := s.getInspectionTranscript(context.Background(), info)
	if err != nil || again.LaunchInfo.String("workDir") == "corrupted caller copy" || faults.closed.Load() != 1 {
		t.Fatal("Cache exposed mutable shared state or reread immutable version", err)
	}
}

func TestInspectionCacheInvalidatesNativeChangeTimeAppendAndProviderRevision(t *testing.T) {
	s, info := inspectionCacheServer(t)
	content := []byte(`{"type":"assistant","uuid":"a1","message":{"role":"assistant","model":"fixture-model","id":"api1","content":"actual response","usage":{"input_tokens":12,"output_tokens":8}}}` + "\n")
	if err := os.WriteFile(info.Path, content, 0600); err != nil {
		t.Fatal(err)
	}
	faults := &recoveryFaultFiles{}
	s.reader = sessionstore.NewReaderWithBudget(faults, s.budget)
	first, err := s.getInspectionTranscript(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(info.Path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err = s.getInspectionTranscript(context.Background(), info); err != nil || faults.closed.Load() != 2 {
		t.Fatal("Native ctime-only change reused stale snapshot", err, faults.closed.Load())
	}
	s.options.InspectionModelOptions.Environment["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"] = `{"fixture-model":1000000}`
	changed, err := s.getInspectionTranscript(context.Background(), info)
	if err != nil || faults.closed.Load() != 3 || bytes.Equal(first.ContextEstimate, changed.ContextEstimate) {
		t.Fatal("Editable context budget reused stale interpretation", err, faults.closed.Load())
	}
	f, err := os.OpenFile(info.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"user","uuid":"later","message":{"role":"user","content":"new message"}}` + "\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	appended, err := s.getInspectionTranscript(context.Background(), info)
	if err != nil || faults.closed.Load() != 4 || string(appended.LaunchInfo.Get("transcriptMessageCount")) != "2" {
		t.Fatal("Append reused old snapshot", err, faults.closed.Load())
	}
	after, err := os.ReadFile(info.Path)
	if err != nil || !bytes.HasPrefix(after, content) {
		t.Fatal("Inspection mutated original bytes", err)
	}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("22222222-2222-4222-8222-%012d", i)
		path := filepath.Join(filepath.Dir(info.Path), id+".jsonl")
		if err = os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.getInspectionTranscript(context.Background(), sessionInfo{ID: id, Path: path}); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.inspectionSnapshots) != 8 || len(s.inspectionSnapshotOrder) != 8 {
		t.Fatal("Source eight-entry insertion cache exceeded", len(s.inspectionSnapshots))
	}
	// Verify cache payloads still have the complete Source JSON envelope.
	for _, pending := range s.inspectionSnapshots {
		if !json.Valid(recoveryJSON(pending.value)) {
			t.Fatal("Invalid cached snapshot")
		}
	}
}
