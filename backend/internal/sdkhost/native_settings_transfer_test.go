package sdkhost

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeSettingsTransferCannotBeFabricatedOrWeakenOrdinaryReferenceGuard(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	h := &Host{options: Options{Owner: owner, NativeParentSettings: func(*cooperative.Scope) (NativeSettingsTransfer, error) { return NativeSettingsTransfer{}, nil }}}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, _, e := h.readASKSettings(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("fabricated immutable values became cache proof", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	first, second := object([]byte(`{"model":"same"}`)), object([]byte(`{"model":"same"}`))
	h = &Host{}
	if e := h.validateASKSettingsReference(&first, &second); e == nil {
		t.Fatal("same JSON weakened existing Source identity guard")
	}
	h = &Host{options: Options{NativeParentSettings: func(*cooperative.Scope) (NativeSettingsTransfer, error) { return NativeSettingsTransfer{}, nil }}}
	if e := h.validateASKSettingsReference(&first, &second); e == nil {
		t.Fatal("unminted parent binding bypassed original identity guard")
	}
}
