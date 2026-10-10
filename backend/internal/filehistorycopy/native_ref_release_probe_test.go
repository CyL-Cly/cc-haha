//go:build cch_native_ref_probe

package filehistorycopy

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

func TestNativeRefReleaseProbe(t *testing.T) {
	for _, closeAtConsumer := range []bool{false, true} {
		name := "normal-release"
		if closeAtConsumer {
			name = "close-at-consumer"
		}
		t.Run(name, func(t *testing.T) {
			owner, service, _, refs, home, _, _ := setup(t, OSFiles{})
			source, target := filepath.Join(home, "original"), filepath.Join(home, "copied")
			literal := []byte("original-private-copy\x00\xff\xe2\x82\xac")
			if e := os.WriteFile(source, literal, 0640); e != nil {
				t.Fatal(e)
			}
			physicalDone := make(chan struct{})
			observed, _, _ := nativerefprobe.Observe(t, owner, refs, closeAtConsumer, func(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
				return service.dispatch(scope, func() error { defer close(physicalDone); return (OSFiles{}).CopyFile(source, target) })
			})
			<-physicalDone
			bytes, e := os.ReadFile(target)
			if e != nil {
				t.Fatal(e)
			}
			if string(bytes) != string(literal) {
				t.Fatal("actual copy bytes changed")
			}
			info, e := os.Stat(target)
			if e != nil || info.Mode().Perm() != 0640 {
				t.Fatal("actual copy mode", e)
			}
			hash := sha256.Sum256(bytes)
			observed.PID, observed.Path, observed.Kind = os.Getpid(), target, "filehistory-copy"
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
