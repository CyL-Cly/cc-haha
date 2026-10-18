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

type materializeReturnObservation struct {
	Phase, PrepareCalls int
	Materialize         string
}

func TestActualSourceMaterializeAsyncReturnResumesBeforeSecondPrepare(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned whole Source required")
	}
	for _, buffered := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "buffered"}[buffered], func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-materialize-return.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			process.Dir = home
			process.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_MATERIALIZE_RETURN=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
			process.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "buffered": buffered}))
			var stderr bytes.Buffer
			process.Stderr = &stderr
			out, err := process.Output()
			if err != nil {
				t.Fatal("full original materialize", err, string(out), stderr.String())
			}
			var source struct {
				Events       []materializeReturnObservation
				PrepareCalls int
				Persisted    bool
				Blocked      []string
			}
			if err = json.Unmarshal(out, &source); err != nil || !source.Persisted || len(source.Blocked) != 0 {
				t.Fatal(err, string(out))
			}
			t.Log("whole original materialize return", string(out))
			owner, writer, pending, loader := uuidHarness(t, "", nil, false)
			writer.mu.Lock()
			writer.path = writer.target()
			path := writer.path
			if buffered {
				entry := object(raw(map[string]any{"type": "queue-operation", "uuid": "native-buffered-materialize", "operation": "enqueue", "content": "real buffered entry", "sessionId": writer.options.SessionID}))
				writer.pending = append(writer.pending, &entry)
			}
			writer.mu.Unlock()
			var recording *RecordFuture
			actual := []materializeReturnObservation{}
			observed := make(chan struct{})
			prepareCalls := 1
			var probe func(*cooperative.Scope, int)
			probe = func(scope *cooperative.Scope, index int) {
				// The first prepare resolves materialize's inner await. Following
				// probes read only the genuine second prepare call count.
				actual = append(actual, materializeReturnObservation{Phase: index, PrepareCalls: prepareCalls})
				if index < 3 {
					_, err := owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, index+1); return nil })
					if err != nil {
						t.Error(err)
					}
				} else {
					close(observed)
				}
			}
			pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
				future := &RecordFuture{owner: owner, future: newWriteFuture()}
				recording = future
				record := plainMessage("native-materialize-return", "actual materialize async-return prompt")
				job := &recordContinuation{pending: pending, loader: loader, future: future, real: true, sid: writer.options.SessionID, stampSID: writer.options.SessionID, inserting: []recordOriginal{{record: record}}, options: RecordOptions{PromptID: func() json.RawMessage { return nil }, ObservePrepare: func(string) { prepareCalls++ }}}
				prepared, err := pending.Prepare(scope, path)
				if err != nil {
					return err
				}
				if err = prepared.future.thenOwner(owner, func(next *cooperative.Scope, failure error) {
					if failure != nil {
						t.Error(failure)
					}
					probe(next, 0)
				}); err != nil {
					return err
				}
				job.wait(prepared.future, job.afterMaterialize)
				return nil
			})
			select {
			case <-observed:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, err = recording.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			want := append([]materializeReturnObservation(nil), source.Events...)
			for i := range want {
				want[i].Materialize = ""
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatal("real materialize caller-return cutpoint differs", actual, want)
			}
		})
	}
}
