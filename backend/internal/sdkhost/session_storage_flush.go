package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// beginFlushSessionStorage preserves the original export's async return R,
// distinct from Project.flush's Q. Callers await R once; neither the generic
// PendingWriter nor Project cleanup gains an additional await.
func (h *Host) beginFlushSessionStorage(scope *cooperative.Scope) (*OwnerFuture[struct{}], error) {
	future, err := h.pending.Flush(scope)
	if err != nil {
		return nil, err
	}
	exported, err := future.Then(scope, func(_ *cooperative.Scope, failure error) error { return failure })
	if err != nil {
		return nil, err
	}
	return h.options.Controller.WrapSDKFlushObserverReturn(scope, exported)
}

// awaitInitialTranscript is QueryEngine.submitMessage's initial persistence
// block. Bare ordinary input keeps the original detached Record; session input
// and nonbare input await Record before applying the initial flush conditions.
func (h *Host) awaitInitialTranscript(scope *cooperative.Scope, record *sessioncontroller.RecordFuture, sessionInput bool) error {
	if h.isAgent() || sessionInput || !costAuthEnvTruthy(h.options.OriginalEnv["CLAUDE_CODE_SIMPLE"]) {
		if _, err := record.Await(scope); err != nil {
			return err
		}
		h.options.OnPhase("Record:fulfilled")
		if sessionInput || costAuthEnvTruthy(h.options.OriginalEnv["CLAUDE_CODE_EAGER_FLUSH"]) || costAuthEnvTruthy(h.options.OriginalEnv["CLAUDE_CODE_IS_COWORK"]) {
			flush, err := h.beginFlushSessionStorage(scope)
			if err != nil {
				return err
			}
			_, err = flush.Await(scope)
			return err
		}
	} else {
		h.observeRecord(scope, record)
	}
	return nil
}
