package climigrations

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeOptions are constructed by the real CLI factory from its existing
// settings/auth/file producers. New's finite, empty-credential contract is
// separate and unchanged. The source token checks the concrete services; the
// ledger identity only binds physical jobs and does not establish OS origin.
type NativeOptions struct {
	Owner                   *cooperative.Owner
	Binding                 *ownerasync.NativeBinding
	Sources                 *clistartupenv.NativeMigrationSources
	Global                  *cliglobalconfig.Service
	ConfigHome, OriginalCWD string
	Now                     func() float64
	Environment             func() map[string]string
	Identity                func(*cooperative.Scope) (modelselection.MetadataIdentity, error)
	ProjectKey              func(*cooperative.Scope) (string, error)
	Writer                  clisettings.NativeSourceWriterBindings
	AutoModeEnabled         func(*cooperative.Scope) (string, error)
	Event                   func(*cooperative.Scope, string, protocol.JSONObject) error
	LogError                func(*cooperative.Scope, error) error
	// These only dispatch the actual mkdir/write primitives. The migration's
	// catch and subsequent Global.Save stay on this same owner.
	MkdirAll       func(*cooperative.Scope, string) (*ownerasync.OwnerFuture[struct{}], error)
	WriteExclusive func(*cooperative.Scope, string, []byte) (*ownerasync.OwnerFuture[struct{}], error)
	Observe        func(Event)
}

type nativeConsumer struct {
	options NativeOptions
	scope   *cooperative.Scope
	failure error
}

func NewNative(scope *cooperative.Scope, options NativeOptions) (*Service, error) {
	if scope == nil || !scope.BelongsTo(options.Owner) || !options.Binding.Owns(options.Owner) || options.Sources == nil || options.Global == nil || !filepath.IsAbs(options.ConfigHome) || !filepath.IsAbs(options.OriginalCWD) || options.Now == nil || options.Environment == nil || options.Identity == nil || options.ProjectKey == nil || options.Writer.StartLocalIgnore == nil || options.AutoModeEnabled == nil || options.Event == nil || options.LogError == nil || options.MkdirAll == nil || options.WriteExclusive == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := options.Sources.Check(scope, options.Owner, options.Global); err != nil {
		return nil, err
	}
	return &Service{options: Options{Global: options.Global, ConfigHome: options.ConfigHome, Now: options.Now, Observe: options.Observe}, native: &nativeConsumer{options: options}}, nil
}

func (s *Service) RunNative(scope *cooperative.Scope) (*ChangelogTask, error) {
	if s == nil || s.native == nil || scope == nil {
		return nil, cooperative.ErrRestricted
	}
	native := s.native
	if err := native.options.Sources.Check(scope, native.options.Owner, s.options.Global); err != nil {
		return nil, err
	}
	if !native.options.Binding.Owns(native.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	native.scope, native.failure = scope, nil
	defer func() { native.scope = nil }()
	return s.run()
}

func (s *Service) nativeIdentity() (modelselection.MetadataIdentity, error) {
	return s.native.options.Identity(s.native.scope)
}

func (s *Service) nativeFirstParty() bool {
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if envTruthy(s.native.options.Environment()[key]) {
			return false
		}
	}
	return true
}

func (s *Service) nativeProDefault() error {
	config, err := s.options.Global.Get()
	if err != nil || truthy(config.Get("opusProMigrationComplete")) {
		return err
	}
	skipped, custom := true, false
	if s.nativeFirstParty() {
		identity, err := s.nativeIdentity()
		if err != nil {
			return err
		}
		skipped = identity.SubscriptionType != "pro"
		if !skipped {
			merged, err := s.native.options.Sources.Merged(s.native.scope)
			if err != nil {
				return err
			}
			custom = merged != nil && merged.Get("model") != nil
		}
	}
	stamp := s.options.Now()
	if err := s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Set("opusProMigrationComplete", []byte("true"))
		if !skipped && !custom {
			next.Set("opusProMigrationTimestamp", []byte(strconv.FormatFloat(stamp, 'f', -1, 64)))
		}
		return &next, nil
	}); err != nil {
		return err
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "skipped", Value: json.RawMessage(strconv.FormatBool(skipped))})
	if !skipped {
		metadata.Set("had_custom_model", json.RawMessage(strconv.FormatBool(custom)))
	}
	// The real additional_metadata exporter serializes the original own-key
	// insertion order. A Go map would put had_custom_model before skipped.
	s.emit(Event{"kind": "event", "name": "tengu_reset_pro_to_opus_default", "data": metadata})
	return nil
}

func (s *Service) nativeSonnet45() error {
	if !s.nativeFirstParty() {
		return nil
	}
	identity, err := s.nativeIdentity()
	if err != nil {
		return err
	}
	if identity.SubscriptionType != "pro" && identity.SubscriptionType != "max" && !(identity.SubscriptionType == "team" && identity.RateLimitTier == "default_claude_max_5x") {
		return nil
	}
	user, err := s.settings("userSettings")
	if err != nil {
		return err
	}
	model := user.Get("model")
	matched := false
	for _, value := range []string{"claude-sonnet-4-5-20250929", "claude-sonnet-4-5-20250929[1m]", "sonnet-4-5-20250929", "sonnet-4-5-20250929[1m]"} {
		if equalString(model, value) {
			matched = true
			break
		}
	}
	if !matched {
		return nil
	}
	var from string
	_ = json.Unmarshal(model, &from)
	has1m := len(from) >= 4 && from[len(from)-4:] == "[1m]"
	to := []byte(`"sonnet"`)
	if has1m {
		to = []byte(`"sonnet[1m]"`)
	}
	if err := s.update("userSettings", protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: to})); err != nil {
		return err
	}
	config, err := s.options.Global.Get()
	if err != nil {
		return err
	}
	var starts float64
	_ = json.Unmarshal(config.Get("numStartups"), &starts)
	if starts > 1 {
		if err := s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("sonnet45To46MigrationTimestamp", []byte(strconv.FormatFloat(s.options.Now(), 'f', -1, 64)))
			return &next, nil
		}); err != nil {
			return err
		}
	}
	s.event("tengu_sonnet45_to_46_migration", map[string]any{"from_model": from, "has_1m": has1m})
	return nil
}

func (s *Service) nativeOpus1m() error {
	if !s.nativeFirstParty() || envTruthy(s.native.options.Environment()["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
		return nil
	}
	identity, err := s.nativeIdentity()
	if err != nil {
		return err
	}
	if identity.SubscriptionType == "pro" || identity.Subscriber && identity.SubscriptionType == "" {
		return nil
	}
	user, err := s.settings("userSettings")
	if err != nil || !equalString(user.Get("model"), "opus") {
		return err
	}
	merged, err := s.native.options.Sources.Merged(s.native.scope)
	if err != nil {
		return err
	}
	settings := protocol.NewJSONObject()
	if merged != nil {
		settings = merged.Clone()
	}
	selector, err := modelselection.NewAnthropicAuthentication(modelselection.Options{Environment: s.native.options.Environment(), Settings: settings}, identity)
	if err != nil {
		return err
	}
	migrated, err := selector.Parse([]byte(`"opus[1m]"`))
	if err != nil {
		return err
	}
	defaultModel, err := selector.Parse(selector.DefaultSetting())
	if err != nil {
		return err
	}
	value := json.RawMessage(`"opus[1m]"`)
	if bytes.Equal(migrated, defaultModel) {
		value = nil
	}
	if err := s.update("userSettings", protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: value})); err != nil {
		return err
	}
	s.event("tengu_opus_to_opus1m_migration", map[string]any{})
	return nil
}

func (s *Service) nativeAutoOffer() error {
	config, err := s.options.Global.Get()
	if err != nil || truthy(config.Get("hasResetAutoModeOptInForDefaultOffer")) {
		return err
	}
	enabled, err := s.native.options.AutoModeEnabled(s.native.scope)
	if err != nil || enabled != "enabled" {
		return err
	}
	user, err := s.settings("userSettings")
	if err != nil {
		return s.caught(err, "Failed to reset auto mode opt-in: ", "", nil)
	}
	var permissions protocol.JSONObject
	_ = permissions.UnmarshalJSON(user.Get("permissions"))
	if truthy(user.Get("skipAutoPermissionPrompt")) && !equalString(permissions.Get("defaultMode"), "auto") {
		if err := s.update("userSettings", protocol.NewJSONObject(protocol.JSONField{Name: "skipAutoPermissionPrompt", Value: nil})); err != nil {
			return s.caught(err, "Failed to reset auto mode opt-in: ", "", nil)
		}
		s.event("tengu_migrate_reset_auto_opt_in_for_default_offer", map[string]any{})
	}
	return s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		if truthy(current.Get("hasResetAutoModeOptInForDefaultOffer")) {
			return current, nil
		}
		next := current.Clone()
		next.Set("hasResetAutoModeOptInForDefaultOffer", []byte("true"))
		return &next, nil
	})
}
