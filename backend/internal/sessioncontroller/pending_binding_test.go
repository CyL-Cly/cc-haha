package sessioncontroller

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"strings"
	"testing"
)

func TestFileHistoryPreActivationPendingBorrowHasOneSDKHost(t *testing.T) {
	owner, writer, pending, _ := uuidHarness(t, "", nil, false)
	same, e := writer.PendingWriter(owner)
	if e != nil || same != pending {
		t.Fatal("pre-activation instance replaced", e)
	}
	if _, e = writer.PendingWriter(owner); e == nil || !strings.Contains(e.Error(), "already has") {
		t.Fatal("second host accepted", e)
	}
	other := cooperative.New()
	defer other.Close()
	if _, e = writer.PendingWriter(other); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e = (*Writer)(nil).PendingWriter(owner); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e = writer.PendingWriter(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
}
func TestFileHistoryPendingBorrowRetainsLegacyFirstConstruction(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	writer := actualPendingWriter(t, owner, t.TempDir(), OSFiles{})
	pending, e := writer.PendingWriter(owner)
	if e != nil {
		t.Fatal(e)
	}
	if writer.async != pending {
		t.Fatal("not original instance")
	}
	if e = pending.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}
