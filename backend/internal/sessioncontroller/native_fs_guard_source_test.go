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
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

type nativeGuardEvent struct {
	Phase    string `json:"phase"`
	Consumed bool   `json:"consumed"`
}

func TestActualSourceFilesystemGuardReturnGraphAndDescriptorBoundaries(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-fs-guard.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	process.Dir = home
	process.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_TRANSCRIPT_FS_GUARD=1", "PATH=" + home}
	input, _ := json.Marshal(map[string]string{"home": home})
	process.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	process.Stderr = &stderr
	output, err := process.Output()
	if err != nil {
		t.Fatal("actual Source filesystem guard", err, string(output), stderr.String())
	}
	var actual struct {
		Results []struct {
			Method         string             `json:"method"`
			Failure        bool               `json:"failure"`
			DistinctReturn bool               `json:"distinctReturn"`
			ErrorSame      bool               `json:"errorSame"`
			ValueSame      bool               `json:"valueSame"`
			Code           string             `json:"code"`
			Events         []nativeGuardEvent `json:"events"`
		} `json:"results"`
		Blocked  []string `json:"blocked"`
		Physical bool     `json:"physical"`
	}
	if err = json.Unmarshal(output, &actual); err != nil || !actual.Physical || len(actual.Blocked) != 0 || len(actual.Results) != 16 {
		t.Fatal("missing actual private native guard cases", err, string(output))
	}
	for _, row := range actual.Results {
		operation := transcriptNativeUnwrapped
		switch row.Method {
		case "mkdir":
			operation = transcriptNativeFSPMkdir
		case "open":
			operation = transcriptNativeFSPOpen
		case "stat":
			operation = transcriptNativeFSPStat
		case "readFile":
			operation = transcriptNativeFSPReadFile
		case "handle.close":
			operation = transcriptNativeGuardedClose
		case "handle.stat", "handle.appendFile", "handle.read":
		default:
			t.Fatal("foreign actual Source method", row.Method)
		}
		guarded := operation != transcriptNativeUnwrapped
		if row.DistinctReturn != guarded || row.Failure && (!row.ErrorSame || row.Code == "") || !row.Failure && !row.ValueSame {
			t.Fatal("native Promise identity/value/rejection changed", row)
		}
		want := []nativeGuardEvent{{"native-ready", false}, {"consumer", true}, {"after-native-reaction", true}, {"after-guard-reaction", true}}
		if guarded {
			want = []nativeGuardEvent{{"native-ready", false}, {"after-native-reaction", false}, {"consumer", true}, {"after-guard-reaction", true}}
		}
		if !reflect.DeepEqual(row.Events, want) {
			t.Fatalf("actual %s failure=%t native/guard/consumer graph: %v != %v", row.Method, row.Failure, row.Events, want)
		}
		for _, bound := range []bool{false, true} {
			events, originalReceipt := observeNativeGuardCompletion(t, operation, bound, row.Failure)
			goWant := want
			if !bound {
				goWant = []nativeGuardEvent{{"native-ready", false}, {"consumer", true}, {"after-native-reaction", true}, {"after-guard-reaction", true}}
			}
			if !reflect.DeepEqual(events, goWant) || originalReceipt != (!bound || !guarded) {
				t.Fatalf("%s bound=%t failure=%t original receipt=%t graph=%v expected=%v", row.Method, bound, row.Failure, originalReceipt, events, goWant)
			}
		}
	}
	t.Log("actual original guard success/rejection and native descriptor receipt", string(output))
}

func observeNativeGuardCompletion(t *testing.T, operation transcriptNativeOperation, bound, rejected bool) ([]nativeGuardEvent, bool) {
	t.Helper()
	owner, controller, serial := parsedSDKHost(t)
	if bound {
		bindAsyncSDKObserverTest(t, owner, controller)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var events []nativeGuardEvent
	consumed := false
	value := &struct{ Label string }{"same native value"}
	failure := errors.New("same actual native rejection")
	var consumedValue *struct{ Label string }
	var consumedFailure error
	var reaction transcriptNativeCompletion
	done := make(chan struct{})
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		events = append(events, nativeGuardEvent{"native-ready", consumed})
		var err error
		reaction, err = completeTranscriptNativeOperation(scope, serial.pending, operation, func(next *cooperative.Scope) {
			if !next.BelongsTo(owner) {
				t.Error("native consumer escaped actual Owner")
			}
			if rejected {
				consumedFailure = failure
			} else {
				consumedValue = value
			}
			consumed = true
			events = append(events, nativeGuardEvent{"consumer", consumed})
		})
		if err != nil {
			return err
		}
		_, err = owner.Microtask(cooperative.Flush, func(*cooperative.Scope) error {
			events = append(events, nativeGuardEvent{"after-native-reaction", consumed})
			_, err := owner.Microtask(cooperative.Flush, func(*cooperative.Scope) error {
				events = append(events, nativeGuardEvent{"after-guard-reaction", consumed})
				close(done)
				return nil
			})
			return err
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = reaction.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if rejected && consumedFailure != failure || !rejected && consumedValue != value {
		t.Fatal("native result/error identity replaced")
	}
	_, originalReceipt := reaction.(*cooperative.Receipt)
	return events, originalReceipt
}

func TestNativeFilesystemGuardRejectsForeignBindingAndInvalidReadRoute(t *testing.T) {
	owner, controller, serial := parsedSDKHost(t)
	bindAsyncSDKObserverTest(t, owner, controller)
	foreign := cooperative.New()
	defer foreign.Close()
	called := false
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := completeTranscriptNativeOperation(scope, serial.pending, transcriptNativeFSPOpen, func(*cooperative.Scope) { called = true })
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign scope admitted", err)
		}
		return nil
	}); err != nil || called {
		t.Fatal(err, called)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		binding := serial.pending.sdkAsyncObservers
		serial.pending.sdkAsyncObservers = &sdkAsyncReturnObservers{controller: controller, writer: controller.writer, owner: foreign}
		_, err := completeTranscriptNativeOperation(scope, serial.pending, transcriptNativeGuardedClose, func(*cooperative.Scope) { called = true })
		serial.pending.sdkAsyncObservers = binding
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign observer binding admitted", err)
		}
		_, err = completeTranscriptNativeOperation(scope, serial.pending, transcriptNativeOperation(255), func(*cooperative.Scope) { called = true })
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("unknown native operation admitted", err)
		}
		return nil
	}); err != nil || called {
		t.Fatal(err, called)
	}
	for _, request := range []sessionhistory.ReadOperation{sessionhistory.ReadOpen, sessionhistory.ReadAt, sessionhistory.ReadClose} {
		op, err := transcriptReadNativeOperation(request, sessionhistory.ReadPrimitiveStream)
		if err != nil || op != transcriptNativeUnwrapped {
			t.Fatal("metadata stream acquired a filesystem guard", request, op, err)
		}
	}
	if _, err := transcriptReadNativeOperation(sessionhistory.ReadAt, sessionhistory.ReadPrimitiveFSFunction); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("invalid fs API/read combination admitted", err)
	}
}
