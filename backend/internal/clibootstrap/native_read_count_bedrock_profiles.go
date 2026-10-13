package clibootstrap

import (
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type nativeBedrockProfileLookup struct {
	done    chan struct{}
	model   *string
	failure error
}
type nativeBedrockProfileState struct {
	mu     sync.Mutex
	values map[string]*nativeBedrockProfileLookup
}

// Source memoizes the original Promise, including an unavailable backing
// model. In-process Agents share the runtime's cache, process workers do not.
func (m *MainPrint) nativeBedrockCountProfile(scope *cooperative.Scope, model string, lookup func() (*string, error)) (*string, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if m.options.readCountProfiles == nil {
		m.options.readCountProfiles = &nativeBedrockProfileState{values: map[string]*nativeBedrockProfileLookup{}}
	}
	state := m.options.readCountProfiles
	state.mu.Lock()
	value, existing := state.values[model]
	if !existing {
		value = &nativeBedrockProfileLookup{done: make(chan struct{})}
		state.values[model] = value
	}
	state.mu.Unlock()
	if !existing {
		value.model, value.failure = lookup()
		close(value.done)
		return value.model, value.failure
	}
	work, err := nativeSkillWork(m, scope, func() (*string, error) { <-value.done; return value.model, value.failure })
	if err != nil {
		return nil, err
	}
	return work.Await(scope)
}
