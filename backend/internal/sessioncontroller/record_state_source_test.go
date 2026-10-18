package sessioncontroller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const writerStateSeed = `{"parentUuid":null,"type":"user","message":{"role":"user","content":"old prompt"},"uuid":"old-user","timestamp":"2023-11-14T22:13:20.000Z"}` + "\n" +
	`{"parentUuid":"old-user","type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"old answer"}]},"uuid":"old-assistant","timestamp":"2023-11-14T22:13:21.000Z"}` + "\n"

type writerStateReceipt struct {
	Profile    string              `json:"profile"`
	Events     []writerObservation `json:"events"`
	Results    []string            `json:"results"`
	Memo       json.RawMessage     `json:"memo"`
	Transcript string              `json:"transcript"`
	Blocked    []string            `json:"blocked"`
	Identities []struct {
		Method string `json:"method"`
		Same   bool   `json:"same"`
	} `json:"identities"`
	Diagnostic     string `json:"diagnostic"`
	SourceIdentity struct {
		SessionStorageSHA256 string `json:"sessionStorageSHA256"`
		Mode                 string `json:"mode"`
	} `json:"sourceIdentity"`
}

func writerStateRecords(t *testing.T) ([]LiveRecord, []LiveRecord) {
	t.Helper()
	var values []LiveRecord
	for _, line := range strings.Split(strings.TrimSpace(writerStateSeed), "\n") {
		value := new(protocol.JSONObject)
		if err := value.UnmarshalJSON([]byte(line)); err != nil {
			t.Fatal(err)
		}
		values = append(values, LiveRecord{Record: value})
	}
	a := append(append([]LiveRecord(nil), values...), LiveRecord{Record: plainMessage("later-id", "later original")})
	b := append(append([]LiveRecord(nil), a...), LiveRecord{Record: plainMessage("next-id", "now original\nnext original")})
	return a, b
}

func nativeWriterState(t *testing.T, profile string, observe bool) writerStateReceipt {
	t.Helper()
	owner, writer, pending, loader := uuidHarness(t, writerStateSeed, nil, false)
	writer.AdoptSessionFile()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := uuidBegin(t, owner, pending, loader, writer.options.SessionID).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	result := writerStateReceipt{Profile: profile, Events: []writerObservation{}}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if pending.observeWriterState != nil {
			return errors.New("normal PendingWriter unexpectedly installed a diagnostic observer")
		}
		if observe {
			pending.observeWriterState = func(value writerObservation) { result.Events = append(result.Events, value) }
		}
		return nil
	})
	a, b := writerStateRecords(t)
	options := RecordOptions{PromptID: func() json.RawMessage { return nil }}
	var first, second *RecordFuture
	if profile == "concurrent" {
		// Like the Source diagnostic, both original calls are admitted in
		// one caller turn, without an await or an intermediate owner task.
		pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
			var err error
			first, err = pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, a, options)
			if err == nil {
				second, err = pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, b, options)
			}
			return err
		})
	} else {
		first = startRecord(t, owner, pending, loader, a, options)
	}
	id, err := first.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result.Results = append(result.Results, text(id))
	if profile == "serial" {
		if err = beginTrackedFlush(t, owner, pending).Wait(ctx); err != nil {
			t.Fatal(err)
		}
		second = startRecord(t, owner, pending, loader, b, options)
	}
	id, err = second.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result.Results = append(result.Results, text(id))
	if err = beginTrackedFlush(t, owner, pending).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Flush, func(*cooperative.Scope) error {
		result.Memo = append(json.RawMessage(nil), writer.lastPrompt...)
		pending.observeWriterState = nil
		return writer.RefreshMetadata()
	})
	if err = beginTrackedFlush(t, owner, pending).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	result.Transcript = recordBytes(t, writer)
	return result
}

func writerStateIDs(values []json.RawMessage) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	return result
}

func validateWriterState(t *testing.T, receipt writerStateReceipt) {
	t.Helper()
	if !reflect.DeepEqual(receipt.Results, []string{"later-id", "next-id"}) || len(receipt.Events) == 0 {
		t.Fatal("actual Record results/events missing", string(raw(receipt)))
	}
	tracked, selections, memos, samples := 0, 0, 0, 0
	for _, event := range receipt.Events {
		if event.SetCount != len(event.Set) {
			t.Fatal("actual Set count differs from copied keys", string(raw(receipt)))
		}
		members := map[string]bool{}
		for _, id := range writerStateIDs(event.Set) {
			if members[id] {
				t.Fatal("observer manufactured duplicate Set entries", string(raw(receipt)))
			}
			members[id] = true
		}
		switch event.Phase {
		case "selection":
			selections++
			want := []string{}
			for _, id := range writerStateIDs(event.Record) {
				if !members[id] {
					want = append(want, id)
				}
			}
			if !reflect.DeepEqual(writerStateIDs(event.Selected), want) {
				t.Fatal("selection differs from actual Set at original selection boundary", string(raw(receipt)))
			}
		case "set-add":
			if !members[string(event.UUID)] {
				t.Fatal("observer ran before actual Set.add", string(raw(receipt)))
			}
		case "tracked-add":
			tracked++
		case "tracked-remove":
			tracked--
		case "memo":
			memos++
			want := "later original"
			if reflect.DeepEqual(writerStateIDs(event.Selected), []string{`"next-id"`}) {
				want = "now original next original"
			}
			if text(event.Memo) != want {
				t.Fatal("memo did not sample this insertion's first meaningful user", string(raw(receipt)))
			}
		case "flush-after-drain":
			samples++
		}
		if tracked < 0 || tracked != event.Tracked {
			t.Fatal("actual trackWrite admission/finally count missing", string(raw(receipt)))
		}
	}
	if tracked != 0 || selections != 2 || memos != 2 || samples == 0 {
		t.Fatal("incomplete inline record/flush observation", string(raw(receipt)))
	}
	rows := []protocol.JSONObject{}
	for _, line := range strings.Split(strings.TrimSpace(receipt.Transcript), "\n") {
		var row protocol.JSONObject
		if err := row.UnmarshalJSON([]byte(line)); err != nil {
			t.Fatal("physical JSONL lost raw row", err, receipt.Transcript)
		}
		rows = append(rows, row)
	}
	counts := map[string]int{}
	last := ""
	for _, row := range rows {
		if id := row.String("uuid"); id != "" {
			counts[id]++
		}
		if row.String("type") == "last-prompt" {
			last = row.String("lastPrompt")
		}
	}
	if counts["old-user"] != 1 || counts["old-assistant"] != 1 || counts["later-id"] < 1 || counts["next-id"] != 1 || last != text(receipt.Memo) {
		t.Fatal("physical rows or final memo changed; duplicate later rows are retained", string(raw(receipt)))
	}
}

func writerStateRecordEvents(values []writerObservation) []writerObservation {
	result := []writerObservation{}
	for _, value := range values {
		if value.Phase == "record" || value.Phase == "selection" || value.Phase == "memo" {
			// Queues/timers are retained in raw. They are not a promise-order
			// assertion in this serial logical-state control.
			value.Queued = 0
			result = append(result, value)
		}
	}
	return result
}

func writerStateJSONEqual(t *testing.T, left, right any) bool {
	t.Helper()
	decode := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal("writer observation is not JSON", err)
		}
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if err = decoder.Decode(&decoded); err != nil {
			t.Fatal("writer observation JSON did not decode", err)
		}
		return decoded
	}
	// A native nil RawMessage and a Source-decoded RawMessage("null")
	// represent the same JSON null. Decode objects structurally while retaining
	// every array element and its order; no event/UUID sorting or deduplication.
	return reflect.DeepEqual(decode(left), decode(right))
}

func TestWriterStateJSONComparisonPreservesOrderedEvidence(t *testing.T) {
	native := writerObservation{Phase: "selection", Record: []json.RawMessage{quote("a"), quote("b")}, Selected: []json.RawMessage{quote("a"), quote("b")}, Set: []json.RawMessage{}, Tracked: 2}
	source := native
	source.UUID, source.Memo = json.RawMessage("null"), json.RawMessage("null")
	if !writerStateJSONEqual(t, native, source) {
		t.Fatal("native nil and decoded Source null are unequal JSON")
	}
	for _, field := range []string{"order", "duplicate", "count", "memo"} {
		changed := source
		switch field {
		case "order":
			changed.Selected = []json.RawMessage{quote("b"), quote("a")}
		case "duplicate":
			changed.Selected = []json.RawMessage{quote("a"), quote("b"), quote("b")}
		case "count":
			changed.Tracked++
		case "memo":
			changed.Memo = quote("different actual memo")
		}
		if writerStateJSONEqual(t, native, changed) {
			t.Fatal("JSON comparison discarded physical evidence", field)
		}
	}
	if writerStateJSONEqual(t, map[string]any{}, map[string]any{"memo": nil}) {
		t.Fatal("JSON comparison discarded a missing field")
	}
	other := native
	other.Phase = "memo"
	if writerStateJSONEqual(t, []writerObservation{native, other}, []writerObservation{other, native}) {
		t.Fatal("JSON comparison reordered events")
	}
	if writerStateJSONEqual(t, json.RawMessage("9007199254740992"), json.RawMessage("9007199254740993")) {
		t.Fatal("JSON comparison rounded distinct numeric evidence")
	}
}

func writerStatePhysicalFields(transcript string) []map[string]json.RawMessage {
	result := []map[string]json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(transcript), "\n") {
		var row map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &row) != nil {
			return nil
		}
		value := map[string]json.RawMessage{}
		for _, field := range []string{"type", "uuid", "parentUuid", "message", "lastPrompt"} {
			if item, ok := row[field]; ok {
				value[field] = item
			}
		}
		result = append(result, value)
	}
	return result
}

func TestActualSourceWriterStateObserverKeepsReturnIdentityAndRawSelections(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-record-state.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(filepath.Dir(fixture), "../../../src/utils/sessionStorage.ts"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(module)
	expectedModule := hex.EncodeToString(digest[:])
	for _, profile := range []string{"serial", "concurrent"} {
		t.Run(profile, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_RECORD_STATE_OBSERVER=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "TEST_ENABLE_SESSION_PERSISTENCE=1", "NODE_ENV=production", "PATH=" + home}
			command.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "profile": profile, "seed": writerStateSeed}))
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatal("whole original writer-state diagnostic", err, string(output), stderr.String())
			}
			var source writerStateReceipt
			if err = json.Unmarshal(output, &source); err != nil || len(source.Blocked) != 0 || source.Diagnostic != "transformed-warm-not-cold-authority" || len(source.Identities) == 0 || source.SourceIdentity.SessionStorageSHA256 != expectedModule || source.SourceIdentity.Mode != "full-original-plus-observer-exports" {
				t.Fatal("original observation proof incomplete", err, string(output))
			}
			for _, identity := range source.Identities {
				if !identity.Same {
					t.Fatal("observer replaced original Promise", string(output))
				}
			}
			validateWriterState(t, source)
			native := nativeWriterState(t, profile, true)
			validateWriterState(t, native)
			t.Log("whole original inline writer raw (not cold authority)", string(output))
			t.Log("native inline writer raw", string(raw(native)))
			if profile == "serial" {
				if text(source.Memo) != "now original next original" || !writerStateJSONEqual(t, writerStateRecordEvents(source.Events), writerStateRecordEvents(native.Events)) {
					t.Fatal("serial actual selection/memo control differs", string(output), string(raw(native)))
				}
			} else {
				// Do not turn OS completion order into an expectation. The full
				// unsorted events expose whether B selected A or A overwrote B.
				t.Log("concurrent diagnostic comparison only; required cold P0 remains independent", writerStateJSONEqual(t, source.Events, native.Events))
			}
		})
	}
}

func TestWriterStateObserverKeepsPrivateSnapshotsAndOwnerGuards(t *testing.T) {
	serial := nativeWriterState(t, "serial", true)
	validateWriterState(t, serial)
	plain := nativeWriterState(t, "serial", false)
	if !reflect.DeepEqual(serial.Results, plain.Results) || !bytes.Equal(serial.Memo, plain.Memo) || !reflect.DeepEqual(writerStatePhysicalFields(serial.Transcript), writerStatePhysicalFields(plain.Transcript)) || len(plain.Events) != 0 {
		t.Fatal("optional observer changed actual record result/physical output", string(raw(serial)), string(raw(plain)))
	}
	owner, writer, pending, loader := uuidHarness(t, writerStateSeed, nil, false)
	writer.AdoptSessionFile()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cache := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
	set, err := cache.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		pending.observeWriterState = func(value writerObservation) {
			calls++
			for _, ids := range [][]json.RawMessage{value.Record, value.Selected, value.Set} {
				for _, id := range ids {
					if len(id) > 0 {
						id[0] = 'X'
					}
				}
			}
			if len(value.Memo) > 0 {
				value.Memo[0] = 'X'
			}
		}
		return nil
	})
	a, _ := writerStateRecords(t)
	future := startRecord(t, owner, pending, loader, a, RecordOptions{PromptID: func() json.RawMessage { return nil }})
	done := future.Done()
	if id, err := future.Wait(ctx); err != nil || text(id) != "later-id" || done != future.Done() {
		t.Fatal("observer changed original public future", string(id), err)
	}
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		if !set.Has(quote("old-user")) || !set.Has(quote("later-id")) || text(writer.lastPrompt) != "later original" || calls == 0 {
			return errors.New("diagnostic snapshot aliased canonical state")
		}
		current, err := pending.SessionUUIDs(scope, loader, writer.options.SessionID)
		if err != nil || current != cache {
			return errors.New("observer replaced cache promise")
		}
		return nil
	})
	foreign := cooperative.New()
	defer foreign.Close()
	before := calls
	if err = foreign.Run(ctx, func(scope *cooperative.Scope) error {
		_, failure := pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, a, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		return failure
	}); !errors.Is(err, cooperative.ErrRestricted) || calls != before {
		t.Fatal("foreign caller reached observation or weakened guard", err, calls, before)
	}
	if err = pending.Close(ctx); err != nil {
		t.Fatal(err)
	}
	before = calls
	if err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, failure := pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, a, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		return failure
	}); !errors.Is(err, ErrPendingClosed) || calls != before {
		t.Fatal("closed admission reached observation or weakened guard", err, calls, before)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { pending.observeWriterState = nil; return nil })
}

func TestWriterStateObserverCapturesHeldInsertAtFlushAfterDrain(t *testing.T) {
	files := newPreparationFiles("stat", "")
	owner, writer, pending, loader := recordHost(t, files, nil)
	t.Cleanup(files.unblock)
	observed := make(chan writerObservation, 1)
	var snapshots []writerObservation
	sampledAfterDrain := false
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		pending.observeWriterState = func(value writerObservation) {
			snapshots = append(snapshots, value)
			if value.Phase == "flush-after-drain" && !sampledAfterDrain {
				sampledAfterDrain = true
				observed <- value // Exactly one send to a one-element buffer.
			}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("held-id", "held original")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
	awaitSignal(t, files.held, "actual preparation fstat completed but unpublished")
	flush := beginTrackedFlush(t, owner, pending)
	for {
		select {
		case value := <-observed:
			if value.Phase != "flush-after-drain" {
				continue
			}
			if value.Tracked != 1 || value.Memo != nil || len(value.Set) != 0 || value.Queued != 0 {
				t.Fatal("afterDrain observer did not sample held original insert", string(raw(value)))
			}
			goto sampled
		case <-ctx.Done():
			t.Fatal("held original flush sample missing", ctx.Err())
		}
	}
sampled:
	select {
	case <-future.Done():
		t.Fatal("observer made held record ready")
	default:
	}
	select {
	case <-flush.Done():
		t.Fatal("observer bypassed tracked flush join")
	default:
	}
	files.hold = ""
	files.unblock()
	if _, err := future.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := flush.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { pending.observeWriterState = nil; return nil })
	t.Log("held actual native preparation/flush raw", string(raw(snapshots)))
	if !strings.Contains(recordBytes(t, writer), `"uuid":"held-id"`) {
		t.Fatal("observation substituted for physical append")
	}
}
