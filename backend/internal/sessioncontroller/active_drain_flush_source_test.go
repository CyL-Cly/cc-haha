package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

type activeFlushConsumerSample struct {
	FirstPersisted bool `json:"firstPersisted"`
	LatePersisted  bool `json:"latePersisted"`
	Tracked        int  `json:"tracked"`
	Queued         int  `json:"queued"`
}

func TestActualSourceActiveDrainFlushJoinsLateProducerBeforeConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun required for active-drain regression")
	}
	sourceHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, err := filepath.Abs("testdata/active-drain-flush.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = sourceHome
	command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_ACTIVE_DRAIN_FLUSH=1", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Join(sourceHome, "bin")}
	input, _ := json.Marshal(map[string]string{"home": sourceHome})
	command.Stdin = bytes.NewReader(input)
	var sourceError bytes.Buffer
	command.Stderr = &sourceError
	sourceRaw, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source active-drain setup: %v raw=%s stderr=%s", err, sourceRaw, sourceError.String())
	}
	var source struct {
		Observation       activeFlushConsumerSample `json:"observation"`
		Registration      []string                  `json:"registration"`
		SameActivePromise bool                      `json:"sameActivePromise"`
		SameFlushPromise  bool                      `json:"sameFlushPromise"`
		FinalPersisted    bool                      `json:"finalPersisted"`
		Physical          bool                      `json:"physical"`
		Blocked           []string                  `json:"blocked"`
		SourceSHA256      string                    `json:"sourceSHA256"`
	}
	if err = json.Unmarshal(sourceRaw, &source); err != nil {
		t.Fatal("actual Source raw", err, string(sourceRaw))
	}
	wantRegistration := []string{"actual-timer-native-drain-started", "original-flush-registered-on-active", "late-producer-registered-on-same-active"}
	want := activeFlushConsumerSample{FirstPersisted: true, LatePersisted: true}
	if sourceError.Len() != 0 || !source.Physical || !source.SameActivePromise || !source.SameFlushPromise || !source.FinalPersisted || len(source.Blocked) != 0 || !reflect.DeepEqual(source.Registration, wantRegistration) || source.Observation != want || source.SourceSHA256 != "bc05983c8ed2164c437f3bf3cb11592a0447ca4ae94605bfa4bff0158f01dac6" {
		t.Fatalf("invalid actual Source active-drain proof: %s stderr=%s", sourceRaw, sourceError.String())
	}
	t.Logf("actual original active-drain flush physical producer receipt %s", sourceRaw)

	owner, writer, pending := makePending(t, OSFiles{})
	files := resumestate.OSFiles{}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: files, Stat: func(path string) (int64, error) {
		info, e := os.Stat(path)
		if e != nil {
			return 0, e
		}
		return info.Size(), nil
	}, ReadFile: os.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return true }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if err != nil {
		t.Fatal(err)
	}
	type admittedLate struct {
		future *WriteFuture
		err    error
	}
	lateAdmission := make(chan admittedLate, 1)
	sample := make(chan activeFlushConsumerSample, 1)
	var flush *FlushFuture
	var observedErr error
	// One legal synchronous Owner turn starts the original timer-admitted drain,
	// registers real Flush, then registers the later producer on the same D.
	// Worker results cannot execute on Owner before this callback returns.
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		active, e := pending.begin(scope, true)
		if e != nil {
			return e
		}
		select {
		case <-active.Done():
			return errors.New("first actual native batch was already settled during admission")
		default:
		}
		flush, e = pending.Flush(scope)
		if e != nil {
			return e
		}
		if e = active.thenOwner(owner, func(next *cooperative.Scope, failure error) {
			if failure != nil {
				lateAdmission <- admittedLate{err: failure}
				return
			}
			entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("custom-title")}, protocol.JSONField{Name: "customTitle", Value: quote("active-drain-late-producer")}, protocol.JSONField{Name: "sessionId", Value: quote(writer.currentSessionID())})
			late, problem := pending.BeginAppend(next, loader, &entry, "")
			lateAdmission <- admittedLate{future: late, err: problem}
		}, func(failure error) { lateAdmission <- admittedLate{err: failure} }); e != nil {
			return e
		}
		_, e = flush.Then(scope, func(_ *cooperative.Scope, failure error) error {
			if failure != nil {
				observedErr = failure
				sample <- activeFlushConsumerSample{}
				return nil
			}
			disk, problem := os.ReadFile(writer.target())
			if problem != nil {
				observedErr = problem
				sample <- activeFlushConsumerSample{}
				return nil
			}
			queued := 0
			writer.mu.Lock()
			for _, entries := range writer.queues {
				queued += len(entries)
			}
			writer.mu.Unlock()
			sample <- activeFlushConsumerSample{FirstPersisted: strings.Contains(string(disk), `"uuid":"native-first"`), LatePersisted: strings.Contains(string(disk), "active-drain-late-producer"), Tracked: pending.tracked, Queued: queued}
			return nil
		})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	var actual activeFlushConsumerSample
	select {
	case actual = <-sample:
	case <-ctx.Done():
		t.Fatal("actual flush consumer did not complete", ctx.Err())
	}
	var late admittedLate
	select {
	case late = <-lateAdmission:
	case <-ctx.Done():
		t.Fatal("actual late producer was not admitted", ctx.Err())
	}
	if late.err != nil || late.future == nil {
		t.Fatal("actual late append admission", late.err)
	}
	if err = late.future.Wait(ctx); err != nil {
		t.Fatal("actual late append join", err)
	}
	if err = flush.Wait(ctx); err != nil {
		t.Fatal("first actual flush join", err)
	}
	// Old RED still joins/drains the genuine late producer before reporting the
	// same business assertion. No abandoned worker, callback, or timer remains.
	if err = beginTrackedFlush(t, owner, pending).Wait(ctx); err != nil {
		t.Fatal("final actual flush join", err)
	}
	if err = pending.Close(ctx); err != nil {
		t.Fatal("physical writer close/join", err)
	}
	final, err := os.ReadFile(writer.target())
	if err != nil || !strings.Contains(string(final), "active-drain-late-producer") {
		t.Fatal("final late bytes absent after physical join", err, string(final))
	}
	if observedErr != nil {
		t.Fatal("actual first flush consumer error", observedErr)
	}
	if actual != source.Observation {
		t.Fatalf("active drain flush returned before its late native producer was persisted: Source=%+v Go=%+v SourceRaw=%s", source.Observation, actual, sourceRaw)
	}
}
