//go:build cch_native_ref_probe

package resumestate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativerefprobe"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"os"
	"path/filepath"
	"testing"
)

type probeActualPlanFiles struct {
	OSFiles
	completed chan struct{}
}

func (files *probeActualPlanFiles) ReadFile(path string) ([]byte, error) {
	defer close(files.completed)
	return files.OSFiles.ReadFile(path)
}
func TestNativeRefReleaseProbe(t *testing.T) {
	for _, closeAtConsumer := range []bool{false, true} {
		name := "normal-release"
		if closeAtConsumer {
			name = "close-at-consumer"
		}
		t.Run(name, func(t *testing.T) {
			files := &probeActualPlanFiles{completed: make(chan struct{})}
			service, owner, _, refs := publicServiceReferences(t, files)
			home, path := service.options.ConfigDir, filepath.Join(service.options.ConfigDir, "original-plan.txt")
			literal := []byte("original-private-plan\x00\xff\xe2\x82\xac")
			if e := os.MkdirAll(home, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(path, literal, 0600); e != nil {
				t.Fatal(e)
			}
			observed, got, inConsumer := nativerefprobe.Observe(t, owner, refs, closeAtConsumer, func(scope *cooperative.Scope) (*ownerasync.OwnerFuture[[]byte], error) {
				return service.nativeRead(scope, path)
			})
			<-files.completed
			if string(got) != string(literal) || len(got) == 0 || &got[0] != &inConsumer[0] {
				t.Fatal("original actual native byte result identity changed")
			}
			physical, e := os.ReadFile(path)
			if e != nil || string(physical) != string(literal) {
				t.Fatal("actual private file changed", e)
			}
			hash := sha256.Sum256(got)
			observed.PID, observed.Path, observed.Kind = os.Getpid(), path, "native-plan-read"
			observed.PhysicalBytesSHA, observed.PhysicalComplete = hex.EncodeToString(hash[:]), true
			retainNativeRefProbe(t, observed, home)
		})
	}
}

func retainNativeRefProbe(t *testing.T, observed nativerefprobe.Observation, home string) {
	t.Helper()
	base := os.Getenv("CC_HAHA_NATIVE_REF_REPORT_DIR")
	if base == "" {
		t.Fatal("probe requires an explicit ignored report directory")
	}
	output, e := json.Marshal(observed)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(base, observed.Kind+"-"+observed.Mode+".json"), append(output, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	// Every actual descriptor is already closed by the native call. A count of 1
	// here is the mechanical ledger leak, not an active FD or filesystem worker.
	if e = os.RemoveAll(home); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(home); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("private filesystem teardown", e)
	}
	if observed.CountBefore != 0 || observed.CountAdmitted != 1 || observed.CountConsumer != 1 || !observed.PhysicalComplete || observed.OriginalFutureError != "" {
		t.Fatalf("physical/settlement prerequisite absent: %+v", observed)
	}
	if observed.Mode == "normal-release" {
		if observed.CountAfter != 0 || !observed.IdleClosed || observed.ReleaseReceiptError != "" || observed.ConsumerReceiptError != "" {
			t.Fatalf("normal release control did not reach zero: %+v", observed)
		}
	} else if observed.CountAfter != 0 || !observed.IdleClosed || observed.ReleaseReceiptError != cooperative.ErrClosed.Error() || observed.ConsumerReceiptError != cooperative.ErrClosed.Error() {
		t.Fatalf("rejected release receipt was not administratively reclaimed: %+v", observed)
	}
}
