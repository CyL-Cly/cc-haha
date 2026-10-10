package modelaccounting

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerlogging"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

// Session is the sole mutable session ledger. Each original request separately
// derives its finite pricing model/environment and original async-chain context.
// Snapshot does not charge; SDK per-ASK message_stop usage is a separate consumer.
type Session struct {
	owner                                            *cooperative.Owner
	queryLog                                         *providerlogging.Queue
	total                                            protocol.JSONObject
	cost                                             float64
	models                                           protocol.JSONObject
	lastRequestID                                    *string
	apiDuration, apiDurationWithoutRetries           float64
	generationDecodeDuration, generationTtftDuration float64
	legacy                                           *requestretry.Input
	nativeCalculator                                 func(string, map[string]string) (*Calculator, error)
	nativeScopedCalculator                           func(*cooperative.Scope, string, map[string]string) (*Calculator, error)
	unknownModelCost                                 bool
	ledgerRelay                                      func(*cooperative.Scope, LedgerDelta) error
	restoredStartMillis                              *float64
	restoredLinesAdded, restoredLinesRemoved         int
	restoredToolDuration                             float64
}
type Attempt struct {
	session        *Session
	usage          protocol.JSONObject
	seen, finished bool
	calculator     *Calculator
	querySource    string
	agentContext   *AgentContextBinding
}

// NewOwnedSession starts original zero accounting with an optional original
// bootstrap lastMainRequestId. It never accepts Source-rendered cost/usage.
func NewOwnedSession(owner *cooperative.Owner, initialLastMainRequestID *string) (*Session, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	q, _ := providerlogging.New(owner)
	s := &Session{owner: owner, queryLog: q, total: ZeroUsage(), models: protocol.NewJSONObject()}
	if initialLastMainRequestID != nil {
		copy := *initialLastMainRequestID
		s.lastRequestID = &copy
	}
	return s, nil
}

// NewSession/Begin preserve the earlier explicitly seeded component API. They
// are not production request provenance. RunQueued uses BeginRequest instead.
func NewSession(owner *cooperative.Owner, model string, env map[string]string) (*Session, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	if _, err := NewCalculator(model, env); err != nil {
		return nil, err
	}
	s, _ := NewOwnedSession(owner, nil)
	input := requestretry.Input{Context: requestcontext.Context{Options: requestcontext.Options{Model: model, QuerySource: "sdk"}, Host: requestcontext.Host{Env: copyEnvironment(env)}}}
	s.legacy = &input
	return s, nil
}
func copyEnvironment(original map[string]string) map[string]string {
	copy := make(map[string]string, len(original))
	for name, value := range original {
		copy[name] = value
	}
	return copy
}
func (s *Session) Owns(owner *cooperative.Owner) bool {
	return s != nil && owner != nil && s.owner == owner
}

func (s *Session) InstallNativeCalculator(scope *cooperative.Scope, factory func(string, map[string]string) (*Calculator, error)) error {
	if s == nil || !scope.BelongsTo(s.owner) || factory == nil {
		return cooperative.ErrRestricted
	}
	if s.nativeCalculator != nil || s.nativeScopedCalculator != nil {
		return unsupported("native accounting process binding already installed")
	}
	s.nativeCalculator = factory
	return nil
}

// Credential-backed pricing resolves its actual cached model identity on the
// current request's owner. The initialization scope must never be retained by
// a later model request's cost callback.
func (s *Session) InstallNativeScopedCalculator(scope *cooperative.Scope, factory func(*cooperative.Scope, string, map[string]string) (*Calculator, error)) error {
	if s == nil || !scope.BelongsTo(s.owner) || factory == nil {
		return cooperative.ErrRestricted
	}
	if s.nativeCalculator != nil || s.nativeScopedCalculator != nil {
		return unsupported("native accounting process binding already installed")
	}
	s.nativeScopedCalculator = factory
	return nil
}

func (s *Session) ValidateNativeModel(scope *cooperative.Scope, model string, env map[string]string) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	if s.nativeScopedCalculator != nil {
		_, err := s.nativeScopedCalculator(scope, model, copyEnvironment(env))
		return err
	}
	factory := s.nativeCalculator
	if factory == nil {
		factory = NewCalculator
	}
	_, err := factory(model, copyEnvironment(env))
	return err
}
func (s *Session) Begin(scope *cooperative.Scope) (*Attempt, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return nil, cooperative.ErrRestricted
	}
	if s.legacy == nil {
		return nil, unsupported("legacy component Begin requires original model/env")
	}
	binding, _ := NewMainContext(s.owner)
	return s.BeginRequest(scope, *s.legacy, binding)
}

// BeginRequest runs on the original producer stack before Start/Prepare. The
// input is the same original input sent to the opener, not response.model,
// a Source-projected price/model or a captured request body. Within the finite
// unknown-relay pricing domain the resolved pricing ID equals options.model;
// known models, aliases/overrides and other cost resolvers remain typed caps.
func (s *Session) BeginRequest(scope *cooperative.Scope, input requestretry.Input, binding *AgentContextBinding) (*Attempt, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return nil, cooperative.ErrRestricted
	}
	if !binding.Owns(s.owner) {
		return nil, unsupported("original same-owner agent context binding")
	}
	if input.ResolvedFastMode != nil && *input.ResolvedFastMode && !(input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile && input.Context.Host.NativeFastModeProof.Owns(s.owner) && s.nativeScopedCalculator != nil) {
		return nil, unsupported("accounting active fast mode")
	}
	factory := NewCalculator
	if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile && s.nativeCalculator != nil {
		factory = s.nativeCalculator
	}
	var calculator *Calculator
	var err error
	if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile && s.nativeScopedCalculator != nil {
		calculator, err = s.nativeScopedCalculator(scope, input.Context.Options.Model, copyEnvironment(input.Context.Host.Env))
	} else {
		calculator, err = factory(input.Context.Options.Model, copyEnvironment(input.Context.Host.Env))
	}
	if err != nil {
		return nil, err
	}
	return &Attempt{session: s, usage: ZeroUsage(), calculator: calculator, querySource: input.Context.Options.QuerySource, agentContext: binding}, nil
}
func (a *Attempt) Observe(scope *cooperative.Scope, source []byte) error {
	if a == nil || a.finished || !scope.BelongsTo(a.session.owner) {
		return cooperative.ErrRestricted
	}
	var event protocol.JSONObject
	if err := event.UnmarshalJSON(source); err != nil {
		return err
	}
	var kind string
	_ = json.Unmarshal(event.Get("type"), &kind)
	var part protocol.JSONObject
	switch kind {
	case "message_start":
		part = object(object(event.Get("message")).Get("usage"))
	case "message_delta":
		part = object(event.Get("usage"))
	default:
		return nil
	}
	usage, err := UpdateUsage(a.usage, part)
	if err != nil {
		return err
	}
	a.usage = usage
	if kind == "message_delta" {
		a.seen = true
	}
	return nil
}
func (a *Attempt) Finish(scope *cooperative.Scope, normal bool, requestID *string) error {
	if a == nil || !scope.BelongsTo(a.session.owner) {
		return cooperative.ErrRestricted
	}
	if a.finished {
		return nil
	}
	a.finished = true
	if a.seen {
		// Other same-owner attempts may have finished since this one began. Attach
		// the current ledger at the actual finally; no stale attempt copy overwrites it.
		a.calculator.Total = a.session.cost
		a.calculator.Models = a.session.models.Clone()
		cost, err := a.calculator.Charge(a.usage)
		if err != nil {
			return err
		}
		a.session.unknownModelCost = a.session.unknownModelCost || a.calculator.UnknownModel
		a.session.cost = a.calculator.Total
		a.session.models = a.calculator.Models
		a.session.total = AccumulateUsage(a.session.total, a.usage)
		if err := a.session.relayLedger(scope, LedgerDelta{kind: "charge", model: a.calculator.Model, cost: cost, usage: a.usage, row: object(a.calculator.Models.Get(a.calculator.Model)), unknown: a.calculator.UnknownModel}); err != nil {
			return err
		}
	}
	// Source's finally charge and later normal-chain ID policy are distinct.
	// Empty response header is false, and active ALS contexts are independent chains.
	if normal && requestID != nil && *requestID != "" && a.agentContext.isMainChain(a.querySource) {
		copy := *requestID
		a.session.lastRequestID = &copy
	}
	return nil
}
func (s *Session) Snapshot(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	out := protocol.NewJSONObject(protocol.JSONField{Name: "totalCostUSD", Value: raw(s.cost)}, protocol.JSONField{Name: "totalInputTokens", Value: s.total.Get("input_tokens")}, protocol.JSONField{Name: "totalOutputTokens", Value: s.total.Get("output_tokens")}, protocol.JSONField{Name: "cacheReadTokens", Value: s.total.Get("cache_read_input_tokens")}, protocol.JSONField{Name: "cacheCreationTokens", Value: s.total.Get("cache_creation_input_tokens")}, protocol.JSONField{Name: "modelUsage", Value: raw(s.models)})
	if s.lastRequestID != nil {
		out.Set("lastMainRequestID", quote(*s.lastRequestID))
	}
	return out, nil
}
