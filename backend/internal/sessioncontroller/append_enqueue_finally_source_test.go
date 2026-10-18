package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type enqueueFinallyObservation struct {
	Kind, Phase, Status string
	Tracked             int
	Added               bool
}

type recordFinallyObservation struct {
	Phase, Memo, Record, Insertion, Track string
	Tracked                               int
}

func TestActualSourceRecordFinallyPreservesAsyncInsertReturnAdoption(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-enqueue-finally.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	process.Dir = home
	process.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_ENQUEUE_FINALLY=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
	process.Stdin = bytes.NewReader(raw(map[string]any{"home": home}))
	var stderr bytes.Buffer
	process.Stderr = &stderr
	output, err := process.Output()
	if err != nil {
		t.Fatal("actual Source recordTranscript/insertMessageChain/trackWrite", err, string(output), stderr.String())
	}
	var source struct {
		RecordEvents []recordFinallyObservation
		RecordUUID   string
		Persisted    bool
		Blocked      []string
	}
	if err := json.Unmarshal(output, &source); err != nil || !source.Persisted || len(source.Blocked) != 0 || source.RecordUUID == "" || len(source.RecordEvents) != 3 {
		t.Fatal("actual Source record receipt differs", err, string(output))
	}
	expected := []recordFinallyObservation{
		{Phase: "tracked-zero-reaction", Tracked: 0, Memo: "actual record finally prompt", Record: "pending", Insertion: "pending", Track: "fulfilled"},
		{Phase: "following-reaction-1", Tracked: 0, Memo: "actual record finally prompt", Record: "pending", Insertion: "fulfilled", Track: "fulfilled"},
		{Phase: "following-reaction-2", Tracked: 0, Memo: "actual record finally prompt", Record: "fulfilled", Insertion: "fulfilled", Track: "fulfilled"},
	}
	for i, value := range expected {
		if source.RecordEvents[i] != value {
			t.Fatal("original async insert return adoption differs", string(output))
		}
	}
	t.Log("actual original record-finally receipt", string(output))
	owner, writer, pending, loader := uuidHarness(t, uuidOld, nil, false)
	writer.AdoptSessionFile()
	if _, err := uuidBegin(t, owner, pending, loader, writer.options.SessionID).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	var recording *RecordFuture
	observed := make(chan recordFinallyObservation, 3)
	var probe func(*cooperative.Scope, int)
	probe = func(scope *cooperative.Scope, index int) {
		status := "pending"
		select {
		case <-recording.Done():
			status = "fulfilled"
		default:
		}
		observed <- recordFinallyObservation{Phase: expected[index].Phase, Tracked: pending.tracked, Memo: text(writer.lastPrompt), Record: status}
		if index < 2 {
			if _, err := owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, index+1); return nil }); err != nil {
				t.Error("record observer reaction", err)
			}
		}
	}
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		zero := newWriteFuture()
		pending.zeroWaiters = append(pending.zeroWaiters, zero)
		if err := zero.thenOwner(owner, func(next *cooperative.Scope, err error) {
			if err != nil {
				t.Error("tracked-zero observer", err)
			}
			probe(next, 0)
		}); err != nil {
			return err
		}
		var err error
		recording, err = pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: plainMessage("native-record-finally", "actual record finally prompt")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		return err
	})
	for i := range expected {
		select {
		case actual := <-observed:
			want := expected[i]
			want.Insertion, want.Track = "", "" // Source-only private Promise identities.
			if actual != want {
				t.Errorf("native Record completion differs: Source=%+v Go=%+v", want, actual)
			}
		case <-ctx.Done():
			t.Fatal("actual Record observer did not run", ctx.Err())
		}
	}
	result, err := recording.Wait(ctx)
	if err != nil || text(result) != "native-record-finally" {
		t.Fatal("actual Record result changed", string(result), err)
	}
	if err := beginTrackedFlush(t, owner, pending).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(writer.target())
	if err != nil || !strings.Contains(string(disk), "native-record-finally") || !strings.Contains(string(disk), "actual record finally prompt") {
		t.Fatal("actual Record did not reach private file", err, string(disk))
	}
}

func TestActualSourceEnqueueFinallySettlesBeforeAppendAwaitAddsUUID(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-enqueue-finally.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	process.Dir = home
	process.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_ENQUEUE_FINALLY=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
	process.Stdin = bytes.NewReader(raw(map[string]any{"home": home}))
	var stderr bytes.Buffer
	process.Stderr = &stderr
	output, err := process.Output()
	if err != nil {
		t.Fatal("actual Source enqueueWrite/trackWrite/appendEntry", err, string(output), stderr.String())
	}
	var source struct {
		Events    []enqueueFinallyObservation
		Persisted bool
		Blocked   []string
	}
	if err := json.Unmarshal(output, &source); err != nil || !source.Persisted || len(source.Blocked) != 0 || len(source.Events) != 6 {
		t.Fatal("actual Source enqueue receipt differs", err, string(output))
	}
	for i, kind := range []string{"user", "queue-operation", "last-prompt"} {
		before, after := source.Events[2*i], source.Events[2*i+1]
		if before.Kind != kind || before.Phase != "tracked-zero-reaction" || before.Tracked != 0 || before.Added || before.Status != "pending" || after.Kind != kind || after.Phase != "append-return" || after.Tracked != 0 || after.Added != (kind == "user") || after.Status != "fulfilled" {
			t.Fatal("original enqueue finally/append await boundaries differ", string(output))
		}
	}
	t.Log("actual original enqueue-finally receipt", string(output))
	for i, kind := range []string{"user", "queue-operation", "last-prompt"} {
		t.Run(kind, func(t *testing.T) {
			owner, writer, pending, loader := uuidHarness(t, uuidOld, nil, false)
			writer.AdoptSessionFile()
			set, err := uuidBegin(t, owner, pending, loader, writer.options.SessionID).Wait(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var future *WriteFuture
			observed := make(chan enqueueFinallyObservation, 1)
			id := "native-enqueue-" + kind
			pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
				zero := newWriteFuture()
				pending.zeroWaiters = append(pending.zeroWaiters, zero)
				if err := zero.thenOwner(owner, func(next *cooperative.Scope, err error) {
					if err != nil {
						t.Error("tracked-zero observer", err)
					}
					added, err := set.OwnerHas(next, quote(id))
					if err != nil {
						t.Error(err)
					}
					status := "pending"
					select {
					case <-future.Done():
						status = "fulfilled"
					default:
					}
					observed <- enqueueFinallyObservation{Kind: kind, Phase: "tracked-zero-reaction", Tracked: pending.tracked, Added: added, Status: status}
				}); err != nil {
					return err
				}
				entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote(kind)}, protocol.JSONField{Name: "uuid", Value: quote(id)}, protocol.JSONField{Name: "operation", Value: quote("enqueue")}, protocol.JSONField{Name: "content", Value: quote("actual native enqueue")})
				var err error
				future, err = pending.BeginAppend(scope, loader, &entry, "")
				return err
			})
			var before enqueueFinallyObservation
			select {
			case before = <-observed:
			case <-ctx.Done():
				t.Fatal("actual enqueue finally observer did not run", ctx.Err())
			}
			if before != source.Events[2*i] {
				t.Errorf("native finally/append reaction differs: Source=%+v Go=%+v", source.Events[2*i], before)
			}
			if err := future.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
				added, err := set.OwnerHas(scope, quote(id))
				if err == nil && added != source.Events[2*i+1].Added {
					t.Error("append-return changed original UUID membership", kind, added)
				}
				return err
			})
			if err := beginTrackedFlush(t, owner, pending).Wait(ctx); err != nil {
				t.Fatal(err)
			}
			disk, err := os.ReadFile(writer.target())
			if err != nil || !strings.Contains(string(disk), id) {
				t.Fatal("actual native enqueue did not reach private file", err, string(disk))
			}
		})
	}
}
