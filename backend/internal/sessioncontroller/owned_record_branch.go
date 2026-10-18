package sessioncontroller

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
)

const OwnedGitRecordProfile = "sdk-shared-owner-git-v1"
const LegacySyncRecordProfile = "component-original-synchronous-branch-v1"

// ValidateSDKRecordProfile checks immutable host wiring without invoking Git,
// sampling CWD or consuming a native HEAD. Bound actual SDK hosts must select
// their capability; an unbound legacy Controller remains the old component API.
func (c *Controller) ValidateSDKRecordProfile() error {
	if c == nil {
		return cooperative.ErrRestricted
	}
	switch c.options.RecordBranchProfile {
	case OwnedGitRecordProfile:
		if err := c.options.BeginOwnedRecordBranch.checkBinding(c.options.Bootstrap); err != nil {
			return err
		}
	case LegacySyncRecordProfile:
		if c.options.BeginOwnedRecordBranch != nil {
			return capability("legacy synchronous and shared-owner Git are mutually exclusive")
		}
	case "":
		if c.options.Bootstrap != nil || c.options.BeginOwnedRecordBranch != nil {
			return capability("bound SDK requires explicit Record branch profile")
		}
	default:
		return capability("unknown SDK Record branch profile")
	}
	return nil
}

func (c *Controller) recordOptions(scope *cooperative.Scope) (RecordOptions, error) {
	options := RecordOptions{PromptID: func() json.RawMessage { return c.promptID }}
	if c.options.TeamContext != nil {
		team, name, _, err := c.TeamWorkerIdentity(scope)
		if err != nil {
			return options, err
		}
		options.TeamName, options.AgentName = raw(team), raw(name)
	}
	if err := c.ValidateSDKRecordProfile(); err != nil {
		return options, err
	}
	if c.options.RecordBranchProfile != OwnedGitRecordProfile {
		return options, nil
	}
	token, err := c.options.Bootstrap.Context(scope)
	if err != nil {
		return options, err
	}
	options.CWDContext = token
	options.Branch = func(next *cooperative.Scope) (RecordBranchFuture, error) {
		return c.options.BeginOwnedRecordBranch.Begin(next, token)
	}
	return options, nil
}

// ResolvedFastModeOnOwner samples the actual migrated AppState field where
// Source query.ts samples getAppState().fastMode. Unknown/active state is not a
// credential/availability/cooldown capability and cannot be silently admitted.
func (c *Controller) ResolvedFastModeOnOwner(scope *cooperative.Scope, proof ...*cooperative.NativeFastModeRequest) (*bool, error) {
	if c == nil || c.options.Bootstrap == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return nil, err
	}
	if !c.active {
		return nil, cooperative.ErrRestricted
	}
	if c.appState.fastMode == nil || *c.appState.fastMode && (len(proof) != 1 || !proof[0].Owns(c.options.Bootstrap.Owner())) {
		return nil, capability("unknown/active fast mode AppState")
	}
	value := *c.appState.fastMode
	return &value, nil
}

// OwnedRecordBranch is a factory-produced capability. Its construction binding
// is private; neither a generic Begin callback nor a noop admission predicate
// can replace the canonical State/Service identity.
type OwnedRecordBranch struct {
	state   *bootstrapstate.State
	service *gitbranch.Service
	// Optional read-only observer of the actual getCachedBranch Promise. It
	// supplies no Scope, readiness, cache value or replacement implementation.
	observeCachedReturn func(*gitbranch.BranchFuture)
}

func (binding *OwnedRecordBranch) checkBinding(state *bootstrapstate.State) error {
	if binding == nil || state == nil || binding.state != state || !binding.service.OwnsOwner(state.Owner()) {
		return capability("shared-owner Git binding unavailable or mismatched")
	}
	return nil
}

// NewOwnedGitRecordBranch verifies only immutable binding before returning.
// Real Branch/HEAD/cache work stays at the original Record continuation callsite.
func NewOwnedGitRecordBranch(state *bootstrapstate.State, service *gitbranch.Service) (*OwnedRecordBranch, error) {
	if state == nil || service == nil {
		return nil, cooperative.ErrRestricted
	}
	binding := &OwnedRecordBranch{state: state, service: service}
	if err := binding.checkBinding(state); err != nil {
		return nil, err
	}
	return binding, nil
}

// Begin retains the actual per-invocation Scope/token checks. Construction
// admission does not make a stale Scope or another async context valid.
func (binding *OwnedRecordBranch) Begin(scope *cooperative.Scope, cwd *bootstrapstate.CWDContext) (RecordBranchFuture, error) {
	if binding == nil || binding.state == nil || binding.service == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := binding.state.CheckScope(scope); err != nil {
		return nil, err
	}
	if !cwd.Owns(binding.state) {
		return nil, cooperative.ErrRestricted
	}
	actual, err := binding.service.BranchWithCWD(scope, func(next *cooperative.Scope) (string, error) { return cwd.GetCWD(next) })
	if err != nil {
		return nil, err
	}
	if binding.observeCachedReturn != nil {
		binding.observeCachedReturn(actual)
	}
	return beginOwnedRecordBranchReturn(scope, binding.state.Owner(), actual)
}
