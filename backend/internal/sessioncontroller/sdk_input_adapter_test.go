package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func parsedSDKHost(t *testing.T) (*cooperative.Owner, *Controller, *SerialASK) {
	t.Helper()
	options := hostOptions(t, t.TempDir()).options
	options.AsyncPersistence = true
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.Activate(activation()); err != nil {
		t.Fatal(err)
	}
	owner := cooperative.New()
	serial, err := NewSerialASK(controller, owner)
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = serial.pending.Close(context.Background())
		owner.Close()
		controller.AwaitBackground()
	})
	return owner, controller, serial
}

func parsedInput(id, content string) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "uuid", Value: quote(id)}, protocol.JSONField{Name: "message", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quote("user")}, protocol.JSONField{Name: "content", Value: quote(content)}))})
}

func releaseParsedIO(t *testing.T, physical *uuidIO) func() {
	t.Helper()
	var once sync.Once
	release := func() { once.Do(func() { close(physical.released) }) }
	t.Cleanup(release)
	return release
}

func TestParsedSDKQueuesFollowerBeforeASKFinallyWithoutAcceptingIt(t *testing.T) {
	owner, controller, serial := parsedSDKHost(t)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		first, err := serial.AdmitParsedSDK(scope, parsedInput("first", "initial"))
		if err != nil {
			return err
		}
		if admission, err := first.Await(scope); err != nil || admission.Duplicate {
			return fmt.Errorf("first admission: %+v/%v", admission, err)
		}
		if accepted, err := serial.BeginDequeued(scope); err != nil || accepted == nil || accepted.String("uuid") != "first" {
			return fmt.Errorf("first dequeued: %v/%v", accepted, err)
		}
		cache := serial.cache
		second, err := serial.AdmitParsedSDK(scope, parsedInput("second", "follow-up"))
		if err != nil {
			return err
		}
		select {
		case <-second.Done():
			return errors.New("UUID/attachment await collapsed to synchronous admission")
		default:
		}
		if _, err = second.Await(scope); err != nil {
			return err
		}
		if !serial.running || serial.cache != cache || len(controller.messages) != 1 || len(controller.queue) != 1 || len(controller.received) != 2 {
			return errors.New("follower admission accepted an ASK or replaced its cache")
		}
		if _, err = serial.BeginDequeued(scope); err == nil {
			return errors.New("two ASK consumers admitted")
		}
		if err = serial.Finally(scope); err != nil {
			return err
		}
		secondMessage, err := serial.BeginDequeued(scope)
		if err != nil || secondMessage == nil || secondMessage.String("uuid") != "second" || len(controller.messages) != 2 {
			return fmt.Errorf("follower dequeued: %v/%v", secondMessage, err)
		}
		if err = serial.Finally(scope); err != nil {
			return err
		}
		if empty, err := serial.BeginDequeued(scope); err != nil || empty != nil || serial.running {
			return errors.New("empty dequeue entered ASK")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestParsedSDKRuntimeDuplicateSurvivesFinallyAndNoUUIDAttachmentPath(t *testing.T) {
	owner, controller, serial := parsedSDKHost(t)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		first, err := serial.AdmitParsedSDK(scope, parsedInput("received", "first"))
		if err != nil {
			return err
		}
		if _, err = first.Await(scope); err != nil {
			return err
		}
		if _, err = serial.BeginDequeued(scope); err != nil {
			return err
		}
		if err = serial.Finally(scope); err != nil {
			return err
		}
		for _, replay := range []bool{true, false} {
			controller.options.ReplayUserMessages = replay
			duplicate, err := serial.AdmitParsedSDK(scope, parsedInput("received", "newer payload"))
			if err != nil {
				return err
			}
			value, err := duplicate.Await(scope)
			if err != nil || !value.Duplicate || value.Historical || (value.Acknowledgement != nil) != replay || len(controller.queue) != 0 {
				return fmt.Errorf("runtime duplicate: %+v/%v", value, err)
			}
		}
		withoutUUID := parsedInput("", "scalar")
		withoutUUID.Delete("uuid")
		f, err := serial.AdmitParsedSDK(scope, withoutUUID)
		if err != nil {
			return err
		}
		select {
		case <-f.Done():
			return errors.New("actual absent attachment await lost")
		default:
		}
		if _, err = f.Await(scope); err != nil {
			return err
		}
		if len(controller.queue) != 1 || len(controller.received) != 1 {
			return errors.New("UUID-free input affected received UUIDs")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestParsedSDKHistoricalLookupUsesActualCacheAndOriginalAck(t *testing.T) {
	owner, controller, serial := parsedSDKHost(t)
	writePrivate(t, controller.writer.target(), uuidOld)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		original := parsedInput("original-old", "duplicate payload")
		original.Set("timestamp", quote("original inbound timestamp"))
		f, err := serial.AdmitParsedSDK(scope, original)
		if err != nil {
			return err
		}
		admission, err := f.Await(scope)
		if err != nil || !admission.Duplicate || !admission.Historical || admission.Acknowledgement == nil {
			return fmt.Errorf("historical admission: %+v/%v", admission, err)
		}
		if admission.Acknowledgement.String("timestamp") != "original inbound timestamp" || string(admission.Acknowledgement.Get("message")) != string(original.Get("message")) || len(controller.queue) != 0 || len(controller.writer.members) != 0 {
			return errors.New("historical acknowledgement/membership differs")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(controller.writer.target())
	if err != nil || string(b) != uuidOld {
		t.Fatal("UUID check changed real transcript", err)
	}
}

func TestParsedSDKRefusesCapabilitiesBeforeOwnerEffects(t *testing.T) {
	for _, name := range []string{"wrong-owner", "unknown-priority", "attachments", "slash", "snapshots", "legacy"} {
		t.Run(name, func(t *testing.T) {
			owner, controller, serial := parsedSDKHost(t)
			other := cooperative.New()
			t.Cleanup(other.Close)
			input := parsedInput("novel", "plain")
			activeOwner := owner
			switch name {
			case "wrong-owner":
				activeOwner = other
			case "unknown-priority":
				input.Set("priority", quote("unknown"))
			case "attachments":
				input.Set("file_attachments", json.RawMessage("[]"))
			case "slash":
				input = parsedInput("novel", "/help")
			case "snapshots":
				controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
			case "legacy":
				controller.options.AsyncPersistence = false
			}
			err := activeOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if _, err := serial.AdmitParsedSDK(scope, input); err == nil {
					return errors.New("unported capability accepted")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(controller.queue) != 0 || len(controller.received) != 0 || serial.primed || serial.running {
				t.Fatal("refusal mutated input/ASK state")
			}
		})
	}
}

func TestParsedSDKThenReturnsActualAdmissionInFIFOReaction(t *testing.T) {
	owner, _, serial := parsedSDKHost(t)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, err := serial.AdmitParsedSDK(scope, parsedInput("novel", "plain"))
		if err != nil {
			return err
		}
		done := make(chan struct{})
		var result Admission
		var observed error
		if err = f.Then(scope, func(_ *cooperative.Scope, value Admission, err error) {
			result, observed = value, err
			close(done)
		}, func(error) {}); err != nil {
			return err
		}
		if err = scope.AwaitPromise(done); err != nil {
			return err
		}
		if observed != nil || result.Duplicate {
			return fmt.Errorf("Then admission: %+v/%v", result, observed)
		}
		if err = f.Then(scope, nil, func(error) {}); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("invalid observer: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestParsedSDKAdmissionSurvivesASKCancelDuringNativeUUIDRead(t *testing.T) {
	owner, controller, serial := parsedSDKHost(t)
	physical := &uuidIO{kind: "readFile", entered: make(chan struct{}), released: make(chan struct{})}
	release := releaseParsedIO(t, physical)
	controller.options.ResumeOptions.Loader = recordLoader(t, physical)
	writePrivate(t, controller.writer.target(), uuidOld)
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original ASK cancelled while session input waits")
	created := make(chan *ParsedSDKFuture, 1)
	runDone := make(chan error, 1)
	go func() {
		runDone <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			f, err := serial.AdmitParsedSDK(scope, parsedInput("after-cancel", "session input"))
			if err != nil {
				return err
			}
			created <- f
			_, err = f.Await(scope)
			return err
		})
	}()
	f := <-created
	awaitSignal(t, physical.entered, "actual input UUID native read")
	cancel(cause)
	if err := <-runDone; !errors.Is(err, cause) {
		t.Fatal("ASK observer lost original cancellation", err)
	}
	select {
	case <-f.Done():
		t.Fatal("ASK cancellation completed independent session admission")
	default:
	}
	release()
	if result, err := f.Wait(context.Background()); err != nil || result.Duplicate {
		t.Fatal("session input did not finish independently", result, err)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if len(controller.queue) != 1 || len(controller.received) != 1 || serial.running || len(controller.messages) != 0 {
			return errors.New("input continuation started ASK after canceled observer")
		}
		return nil
	})
}

func TestParsedSDKOwnerCloseRejectsUnsettledInputWithoutEnqueue(t *testing.T) {
	owner, controller, serial := parsedSDKHost(t)
	physical := &uuidIO{kind: "readFile", entered: make(chan struct{}), released: make(chan struct{})}
	release := releaseParsedIO(t, physical)
	controller.options.ResumeOptions.Loader = recordLoader(t, physical)
	writePrivate(t, controller.writer.target(), uuidOld)
	var f *ParsedSDKFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		f, err = serial.AdmitParsedSDK(scope, parsedInput("after-close", "session input"))
		return err
	})
	awaitSignal(t, physical.entered, "unsettled input UUID native read")
	owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := f.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
		release()
		t.Fatal("dependent admission stranded or got wrong close error", err)
	}
	if len(controller.queue) != 0 || len(controller.received) != 0 {
		release()
		t.Fatal("business input ran after Owner.Close")
	}
	release()
}

func TestParsedSDKUUIDPhysicalReadFailureUsesSourceEmptyMembership(t *testing.T) {
	owner, controller, serial := parsedSDKHost(t)
	controller.options.ResumeOptions.Loader = recordLoader(t, &uuidIO{fault: "readFile"})
	writePrivate(t, controller.writer.target(), uuidOld)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, err := serial.AdmitParsedSDK(scope, parsedInput("original-old", "scalar"))
		if err != nil {
			return err
		}
		admission, err := f.Await(scope)
		if err != nil || admission.Duplicate || len(controller.queue) != 1 || len(controller.received) != 1 {
			return fmt.Errorf("Source load read catch did not yield empty membership: %+v/%v", admission, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
