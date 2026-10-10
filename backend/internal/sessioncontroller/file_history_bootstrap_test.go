package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistorycopy"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

func TestFileHistoryConcreteFactoryBeforeActivationAndOnlyOneHostClaim(t *testing.T) {
	base, _, owner := boundController(t)
	options := base.options
	options.AsyncPersistence = true
	options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
	diagnostics := []sessionresume.Diagnostic{}
	options.ResumeOptions.OnDiagnostic = func(value sessionresume.Diagnostic) { diagnostics = append(diagnostics, value) }
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	pending := controller.writer.async
	if pending == nil || pending.owner != owner || pending.writer != controller.writer || controller.active || pending.hostClaimed {
		t.Fatal("actual pre-activation constructor did not retain one unclaimed Writer/Owner")
	}
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		// This original empty helper input follows the genuine enable/getter/early
		// return path. It is not a mocked resume producer or prepared transcript.
		future, err := controller.resume.BeginCopy(scope, &filehistorycopy.Log{Snapshots: &filehistorycopy.SnapshotArray{}})
		if err != nil {
			return err
		}
		if _, err = future.Await(scope); err != nil {
			return err
		}
		controller.resume.DiagnosticForCopy("event", "original diagnostic", json.RawMessage(`{"numSnapshots":0}`))
		if _, err = controller.resume.Load(resumestate.Input{Source: json.RawMessage(`{"messages":[],"fileHistorySnapshots":[]}`)}); err == nil || !strings.Contains(err.Error(), "owned public resume") {
			t.Error("unsupported public producer was silently emulated", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Name != "original diagnostic" || string(diagnostics[0].Fields) != `{"numSnapshots":0}` {
		t.Fatal("factory diagnostic did not use the original service", diagnostics)
	}
	claimed, err := controller.writer.PendingWriter(owner)
	if err != nil || claimed != pending {
		t.Fatal("SDK substituted another Writer/ledger", err)
	}
	if _, err = controller.writer.PendingWriter(owner); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Fatal("duplicate Host borrowed the same Writer", err)
	}
}

func TestFileHistoryUnboundExportCannotBypassActualFactory(t *testing.T) {
	var service *resumestate.Service
	if err := service.BindOwnedFileHistory(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if _, err := service.BeginCopy(nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	controller, _, owner := boundController(t)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := controller.resume.BeginCopy(scope, &filehistorycopy.Log{})
		return err
	}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("legacy controller gained an unbound async copy", err)
	}
}
