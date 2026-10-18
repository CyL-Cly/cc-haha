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
)

type singleMapVisitSample struct {
	FirstPersisted bool `json:"firstPersisted"`
	LatePersisted  bool `json:"latePersisted"`
	Tracked        int  `json:"tracked"`
	Queued         int  `json:"queued"`
	PathPresent    bool `json:"pathPresent"`
	Active         bool `json:"active"`
	TimerPending   bool `json:"timerPending"`
}

type singleMapVisitResult struct {
	Sample singleMapVisitSample
	Err    error
}

func TestActualSourceSamePathFirstDrainVisitsExistingMapEntryOnlyOnce(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-same-path-map-single-visit.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MAP_SINGLE_VISIT=1", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Join(home, "bin")}
	input, _ := json.Marshal(map[string]string{"home": home})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source single-visit setup: %v raw=%s stderr=%s", err, raw, stderr.String())
	}
	var source struct {
		Observation singleMapVisitSample `json:"observation"`
		Admission   struct {
			CachedIdentitySame        bool `json:"cachedIdentitySame"`
			SameQueueObject           bool `json:"sameQueueObject"`
			SameQueueObjectAtConsumer bool `json:"sameQueueObjectAtConsumer"`
			SamePathOnly              bool `json:"samePathOnly"`
			FirstDNativePending       bool `json:"firstDNativePending"`
			QueueBeforeLate           int  `json:"queueBeforeLate"`
			QueueAfterLate            int  `json:"queueAfterLate"`
			ScheduledLateTimer        bool `json:"scheduledLateTimer"`
		} `json:"admission"`
		Registration    []string `json:"registration"`
		SourceSHA256    string   `json:"sourceSHA256"`
		Physical        bool     `json:"physical"`
		FinalPersisted  bool     `json:"finalPersisted"`
		FinalQueued     int      `json:"finalQueued"`
		FinalTracked    int      `json:"finalTracked"`
		FinalTimerCount int      `json:"finalTimerCount"`
		Blocked         []string `json:"blocked"`
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal("actual Source raw", err, string(raw))
	}
	want := singleMapVisitSample{FirstPersisted: true, Tracked: 0, Queued: 1, PathPresent: true, TimerPending: true}
	if stderr.Len() != 0 || source.Observation != want || !source.Admission.CachedIdentitySame || !source.Admission.SameQueueObject || !source.Admission.SameQueueObjectAtConsumer || !source.Admission.SamePathOnly || !source.Admission.FirstDNativePending || source.Admission.QueueBeforeLate != 0 || source.Admission.QueueAfterLate != 1 || !source.Admission.ScheduledLateTimer || !source.Physical || !source.FinalPersisted || source.FinalQueued != 0 || source.FinalTracked != 0 || source.FinalTimerCount != 0 || len(source.Blocked) != 0 || source.SourceSHA256 != "bc05983c8ed2164c437f3bf3cb11592a0447ca4ae94605bfa4bff0158f01dac6" || !reflect.DeepEqual(source.Registration, []string{"original-timer-firstD-returned-pending", "same-turn-cached-late-append-enqueued"}) {
		t.Fatalf("invalid actual Source single-visit receipt: %s stderr=%s", raw, stderr.String())
	}
	t.Logf("actual original same-path Map visit physical receipt %s", raw)
	owner, writer, pending := makePending(t, OSFiles{})
	observed := make(chan singleMapVisitResult, 1)
	var drain *WriteFuture
	var target string
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		target = writer.target()
		writer.mu.Lock()
		queue, present := writer.queues[target]
		_, cached := writer.identities[target]
		warm := present && len(queue) == 1 && len(writer.queues) == 1 && cached && writer.stop != nil
		writer.mu.Unlock()
		if !warm {
			return errors.New("real warmed first-only Go queue/identity/timer required")
		}
		var problem error
		drain, problem = pending.begin(scope, true)
		if problem != nil {
			return problem
		}
		select {
		case <-drain.Done():
			return errors.New("firstD settled during same-turn admission")
		default:
		}
		writer.mu.Lock()
		queue, present = writer.queues[target]
		spliced := present && len(queue) == 0 && writer.stop == nil
		writer.mu.Unlock()
		if !spliced {
			return errors.New("firstD did not splice existing Go path entry")
		}
		late := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("custom-title")}, protocol.JSONField{Name: "customTitle", Value: quote("map-single-visit-late")}, protocol.JSONField{Name: "sessionId", Value: quote(writer.currentSessionID())})
		if problem = writer.Append(&late); problem != nil {
			return problem
		}
		writer.mu.Lock()
		queue, present = writer.queues[target]
		lateAdmitted := present && len(queue) == 1 && len(writer.queues) == 1 && writer.stop != nil
		writer.mu.Unlock()
		if !lateAdmitted {
			return errors.New("same-turn cached Go late append not enqueued with new timer")
		}
		return drain.thenOwner(owner, func(_ *cooperative.Scope, failure error) {
			if failure != nil {
				observed <- singleMapVisitResult{Err: failure}
				return
			}
			disk, problem := os.ReadFile(target)
			if problem != nil {
				observed <- singleMapVisitResult{Err: problem}
				return
			}
			writer.mu.Lock()
			queued := 0
			for _, entries := range writer.queues {
				queued += len(entries)
			}
			_, present := writer.queues[target]
			sample := singleMapVisitSample{FirstPersisted: strings.Contains(string(disk), `"uuid":"native-first"`), LatePersisted: strings.Contains(string(disk), "map-single-visit-late"), Tracked: pending.tracked, Queued: queued, PathPresent: present, Active: pending.active != nil, TimerPending: writer.stop != nil}
			writer.mu.Unlock()
			observed <- singleMapVisitResult{Sample: sample}
		}, func(failure error) { observed <- singleMapVisitResult{Err: failure} })
	})
	var actual singleMapVisitResult
	if err == nil {
		select {
		case actual = <-observed:
		case <-ctx.Done():
			actual.Err = ctx.Err()
		}
	} else {
		actual.Err = err
	}
	// Drain and join real IO before any final business failure. This is not a
	// delete/reinsert case and never introduces a manual-flush error.
	var final *FlushFuture
	joinErr := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var problem error
		final, problem = pending.Flush(scope)
		return problem
	})
	if joinErr == nil {
		joinErr = final.Wait(context.Background())
	}
	closeErr := pending.Close(context.Background())
	owner.Close()
	if joinErr != nil || closeErr != nil || actual.Err != nil {
		t.Fatalf("physical firstD/final flush/Close error: observation=%v join=%v close=%v", actual.Err, joinErr, closeErr)
	}
	if drain == nil || drain.Wait(context.Background()) != nil {
		t.Fatal("original firstD did not physically settle")
	}
	finalDisk, problem := os.ReadFile(target)
	if problem != nil || !strings.Contains(string(finalDisk), "map-single-visit-late") {
		t.Fatalf("final real late bytes absent after join: %v %s", problem, finalDisk)
	}
	encoded, _ := json.Marshal(actual.Sample)
	t.Logf("actual Go same-path firstD physical receipt %s", encoded)
	if actual.Sample != source.Observation {
		t.Fatalf("same-path first drain consumed an enqueue for an already-visited Map entry: Source=%+v Go=%+v SourceRaw=%s", source.Observation, actual.Sample, raw)
	}
}
