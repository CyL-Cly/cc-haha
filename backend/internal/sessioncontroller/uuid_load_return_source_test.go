package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type uuidLoadReturnObservation struct {
	Phase  int
	Cached string
}

func TestActualSourceSessionUUIDCacheSettlesAfterTranscriptAndSessionAsyncReturns(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned whole Source required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-uuid-load-return.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_UUID_LOAD_RETURN=1", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
	command.Stdin = bytes.NewReader(raw(map[string]string{"home": home, "content": uuidOld}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, e := command.Output()
	if e != nil {
		t.Fatal("actual original UUID load return", e, string(output), stderr.String())
	}
	var source struct {
		Events []struct {
			Phase                       int
			Transcript, Session, Cached string
		}
		Keys      []string
		Coalesced bool
		Blocked   []string
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Blocked) != 0 || !source.Coalesced || !reflect.DeepEqual(source.Keys, []string{"original-old"}) || len(source.Events) != 4 {
		t.Fatal("original UUID return receipt", e, string(output))
	}
	t.Log("whole original UUID load", string(output))
	owner, writer, pending, loader := uuidHarness(t, uuidOld, nil, false)
	var cached *SessionUUIDFuture
	actual, expected := []uuidLoadReturnObservation{}, []uuidLoadReturnObservation{}
	for _, event := range source.Events {
		if event.Transcript != "fulfilled" {
			t.Fatal("original actual transcript return not fulfilled", event)
		}
		expected = append(expected, uuidLoadReturnObservation{event.Phase, event.Cached})
	}
	observed := make(chan struct{})
	var probe func(int)
	probe = func(phase int) {
		status := "pending"
		select {
		case <-cached.Done():
			status = "fulfilled"
		default:
		}
		actual = append(actual, uuidLoadReturnObservation{phase, status})
		if phase < 3 {
			if _, e := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { probe(phase + 1); return nil }); e != nil {
				t.Error(e)
			}
		} else {
			close(observed)
		}
	}
	pending.observeUUIDLoadReturn = func() {
		if _, e := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { probe(0); return nil }); e != nil {
			t.Error(e)
		}
	}
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		var e error
		cached, e = pending.SessionUUIDs(scope, loader, writer.options.SessionID)
		if e != nil {
			return e
		}
		same, e := pending.SessionUUIDs(scope, loader, writer.options.SessionID)
		if same != cached {
			t.Error("real memoized cache identity changed")
		}
		return e
	})
	set, e := cached.Wait(ctx)
	if e != nil || !set.Has(json.RawMessage(`"original-old"`)) {
		t.Fatal("real UUID load lost actual physical user", set, e)
	}
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("actual loadTranscriptFile→loadSessionFile→getSessionMessages returns collapsed: Go=%+v Source=%+v", actual, expected)
	}
}
