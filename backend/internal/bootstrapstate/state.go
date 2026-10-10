package bootstrapstate

import (
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"golang.org/x/text/unicode/norm"
)

var ErrStringDomain = errors.New("bootstrap state requires UTF-8 strings; raw UTF-16 surrogate inputs are not admitted")

func valid(value string) error {
	if !utf8.ValidString(value) {
		return ErrStringDomain
	}
	return nil
}

type Snapshot struct {
	SessionID                     string
	ParentSessionID               *string
	SessionProjectDir             *string
	OriginalCWD, ProjectRoot, CWD string
}

type SlugEntry struct{ SessionID, Slug string }

// PlanSlugs keeps the original cache object identity and Map insertion order.
type PlanSlugs struct {
	owner   *cooperative.Owner
	entries []SlugEntry
}

func (cache *PlanSlugs) Set(scope *cooperative.Scope, id, slug string) error {
	if cache == nil {
		return cooperative.ErrRestricted
	}
	if err := check(scope, cache.owner); err != nil {
		return err
	}
	if err := valid(id); err != nil {
		return err
	}
	if err := valid(slug); err != nil {
		return err
	}
	for index := range cache.entries {
		if cache.entries[index].SessionID == id {
			cache.entries[index].Slug = slug
			return nil
		}
	}
	cache.entries = append(cache.entries, SlugEntry{id, slug})
	return nil
}
func (cache *PlanSlugs) Delete(scope *cooperative.Scope, id string) (bool, error) {
	if cache == nil {
		return false, cooperative.ErrRestricted
	}
	if err := check(scope, cache.owner); err != nil {
		return false, err
	}
	for index, entry := range cache.entries {
		if entry.SessionID == id {
			copy(cache.entries[index:], cache.entries[index+1:])
			cache.entries[len(cache.entries)-1] = SlugEntry{}
			cache.entries = cache.entries[:len(cache.entries)-1]
			return true, nil
		}
	}
	return false, nil
}
func (cache *PlanSlugs) Clear(scope *cooperative.Scope) error {
	if cache == nil {
		return cooperative.ErrRestricted
	}
	if err := check(scope, cache.owner); err != nil {
		return err
	}
	cache.entries = nil
	return nil
}
func (cache *PlanSlugs) Entries(scope *cooperative.Scope) ([]SlugEntry, error) {
	if cache == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := check(scope, cache.owner); err != nil {
		return nil, err
	}
	return append([]SlugEntry{}, cache.entries...), nil
}

type State struct {
	owner                                          *cooperative.Owner
	switches                                       *Signal[string]
	slugs                                          *PlanSlugs
	uuid                                           func() (string, error)
	mu                                             sync.RWMutex // Immutable snapshots for existing host getter boundaries.
	value                                          Snapshot
	hasExitedPlanMode, needsPlanModeExitAttachment bool
	needsAutoModeExitAttachment                    bool
}

// Initial values are supplied by the startup host's real UUID and resolved cwd.
// Constructor admission does not replace cwd resolution or allocate an Owner.
func New(owner *cooperative.Owner, initialID, initialCWD string, uuid func() (string, error)) (*State, error) {
	if owner == nil || uuid == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := valid(initialID); err != nil {
		return nil, err
	}
	if err := valid(initialCWD); err != nil {
		return nil, err
	}
	signal, _ := NewSignal[string](owner)
	cwd := norm.NFC.String(initialCWD)
	return &State{owner: owner, switches: signal, slugs: &PlanSlugs{owner: owner}, uuid: uuid, value: Snapshot{SessionID: initialID, OriginalCWD: cwd, ProjectRoot: cwd, CWD: cwd}}, nil
}
func (state *State) Owns(owner *cooperative.Owner) bool { return state != nil && state.owner == owner }
func (state *State) CheckScope(scope *cooperative.Scope) error {
	if state == nil {
		return cooperative.ErrRestricted
	}
	return check(scope, state.owner)
}
func pointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// Snapshot returns copied immutable values, never a mutable state/cache handle.
// All mutations and listener calls still require this state's original Owner.
func (state *State) Snapshot() Snapshot {
	state.mu.RLock()
	defer state.mu.RUnlock()
	value := state.value
	value.ParentSessionID = pointer(value.ParentSessionID)
	value.SessionProjectDir = pointer(value.SessionProjectDir)
	return value
}
func (state *State) SessionID() string { return state.Snapshot().SessionID }
func (state *State) OnSessionSwitch(scope *cooperative.Scope, listener *Listener[string]) (Unsubscribe, error) {
	if state == nil {
		return nil, cooperative.ErrRestricted
	}
	return state.switches.Subscribe(scope, listener)
}
func (state *State) PlanSlugCache(scope *cooperative.Scope) (*PlanSlugs, error) {
	if state == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := check(scope, state.owner); err != nil {
		return nil, err
	}
	return state.slugs, nil
}
func (state *State) SwitchSession(scope *cooperative.Scope, id string, projectDir *string) error {
	if state == nil {
		return cooperative.ErrRestricted
	}
	if err := check(scope, state.owner); err != nil {
		return err
	}
	if err := valid(id); err != nil {
		return err
	}
	if projectDir != nil {
		if err := valid(*projectDir); err != nil {
			return err
		}
	}
	_, _ = state.slugs.Delete(scope, state.SessionID())
	state.mu.Lock()
	state.value.SessionID = id
	state.value.SessionProjectDir = pointer(projectDir)
	state.mu.Unlock()
	return state.switches.Emit(scope, id)
}
func (state *State) RegenerateSessionID(scope *cooperative.Scope, setCurrentAsParent bool) (string, error) {
	if state == nil {
		return "", cooperative.ErrRestricted
	}
	if err := check(scope, state.owner); err != nil {
		return "", err
	}
	id := state.SessionID()
	if setCurrentAsParent {
		state.mu.Lock()
		state.value.ParentSessionID = &id
		state.mu.Unlock()
	}
	_, _ = state.slugs.Delete(scope, id)
	next, err := state.uuid()
	if err != nil {
		return "", err
	}
	if err = valid(next); err != nil {
		return "", err
	}
	state.mu.Lock()
	state.value.SessionID = next
	state.value.SessionProjectDir = nil
	state.mu.Unlock()
	return next, nil // Source does not emit a session-switched signal here.
}
func (state *State) setCWD(scope *cooperative.Scope, target *string, value string) error {
	if err := check(scope, state.owner); err != nil {
		return err
	}
	if err := valid(value); err != nil {
		return err
	}
	state.mu.Lock()
	*target = norm.NFC.String(value)
	state.mu.Unlock()
	return nil
}
func (state *State) SetOriginalCWD(scope *cooperative.Scope, value string) error {
	if state == nil {
		return cooperative.ErrRestricted
	}
	return state.setCWD(scope, &state.value.OriginalCWD, value)
}
func (state *State) SetProjectRoot(scope *cooperative.Scope, value string) error {
	if state == nil {
		return cooperative.ErrRestricted
	}
	return state.setCWD(scope, &state.value.ProjectRoot, value)
}
func (state *State) SetCWD(scope *cooperative.Scope, value string) error {
	if state == nil {
		return cooperative.ErrRestricted
	}
	return state.setCWD(scope, &state.value.CWD, value)
}
