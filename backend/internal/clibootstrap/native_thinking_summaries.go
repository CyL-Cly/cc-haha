package clibootstrap

import (
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// Source getAllModelBetas is memoized in one CLI runtime. In-process children
// copy the parent options and share this cache; independent roots and process
// workers start with their own cache and global interactive mode.
type nativeThinkingSummaryState struct {
	mu          sync.Mutex
	interactive bool
	values      map[string]bool
}

func (m *MainPrint) nativeThinkingSummaryBeta(scope *cooperative.Scope, model string) (bool, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || m.options.thinkingSummaryState == nil {
		return false, cooperative.ErrRestricted
	}
	state := m.options.thinkingSummaryState
	environment := m.nativeOpenAIEnvironment()
	if !requestcontext.NativeThinkingSummaryUsesCache(environment) {
		return false, nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if value, present := state.values[model]; present {
		return value, nil
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return false, err
	}
	value := requestcontext.NativeThinkingSummaryBeta(model, environment, state.interactive, *settings)
	state.values[model] = value
	return value, nil
}
