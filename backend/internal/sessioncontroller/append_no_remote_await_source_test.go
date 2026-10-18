package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceNoRemoteAppendAwaitsFulfilledPersistenceBeforeReturning(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-no-remote-await.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	process.Dir = home
	process.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NO_REMOTE_APPEND=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
	process.Stdin = bytes.NewReader(raw(map[string]any{"home": home}))
	var stderr bytes.Buffer
	process.Stderr = &stderr
	output, err := process.Output()
	if err != nil {
		t.Fatal("actual Source Project.appendEntry", err, string(output), stderr.String())
	}
	var observed struct {
		Events    []struct{ Phase, Status string }
		Persisted bool
		Blocked   []string
	}
	if err := json.Unmarshal(output, &observed); err != nil || !observed.Persisted || len(observed.Blocked) != 0 || len(observed.Events) != 3 || observed.Events[0].Status != "fulfilled" || observed.Events[1].Phase != "before-remote-await-reaction" || observed.Events[1].Status != "pending" || observed.Events[2].Status != "fulfilled" {
		t.Fatal("Source actual no-remote async boundary differs", err, string(output))
	}
	owner, writer, pending, loader := uuidHarness(t, uuidOld, nil, false)
	writer.AdoptSessionFile()
	cache := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
	set, err := cache.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var future *WriteFuture
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		token, err := pending.BeginTrackedWrite(scope)
		if err != nil {
			return err
		}
		entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "uuid", Value: quote("no-remote-new")})
		future = newWriteFuture()
		// Exercise the actual completion boundary with the canonical writer's
		// real warmed UUID set and tracked token. Full BeginAppend/physical
		// file and CCR/ingress consumers are separate regression tests.
		job := &appendContinuation{pending: pending, entry: &entry, sid: writer.options.SessionID, set: set, add: true, token: token, future: future}
		job.complete(scope, nil)
		select {
		case <-future.Done():
			t.Error("actual no-remote append returned in the persistence admission callback; Source still awaits its fulfilled remote Promise")
		default:
		}
		return nil
	})
	if err := future.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log(string(output))
}

func TestNoRemoteAwaitPreservesDuplicateSidechainAndNonTranscriptBypasses(t *testing.T) {
	for _, tc := range []struct {
		name, kind       string
		added, sidechain bool
	}{
		{"duplicate-user", "user", false, false},
		{"new-agent-sidechain", "user", true, true},
		{"queue-operation", "queue-operation", true, false},
		{"metadata", "last-prompt", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, writer, pending, _ := uuidHarness(t, uuidOld, nil, false)
			pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
				entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote(tc.kind)}, protocol.JSONField{Name: "uuid", Value: quote("bypass-new")})
				if tc.sidechain {
					entry.Set("isSidechain", json.RawMessage(`true`))
					entry.Set("agentId", quote("actual-agent"))
				}
				job := &appendContinuation{pending: pending, entry: &entry, sid: writer.options.SessionID, add: tc.added, future: newWriteFuture()}
				if job.persistRemote(scope) {
					t.Error("Source branch which does not call persistToRemote acquired a new await", tc.name)
				}
				return nil
			})
		})
	}
}
