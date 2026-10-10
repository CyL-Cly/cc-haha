package clistartupenv

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const EmptyPolicyReleaseProfile = "cli-startup-pretrust-external-absent-policy-no-plugins-v1"
const ResolvedPolicyReleaseProfile = "cli-startup-resolved-native-policy-v1"

type Host struct {
	Profile string
	// These are explicit resolved external snapshots, never inferred from the
	// developer's native credentials, MDM, remote cache or system policy files.
	RemoteCacheAbsent, MDMCacheAbsent, HKCUCacheAbsent, PolicyFilesAbsent, PluginBaseAbsent bool
	AllowedSources                                                                          []string // nil is the original default; empty is isolation.
	FlagPath                                                                                *string
	FlagInline                                                                              json.RawMessage
	// ResolvePolicy reads native policy sources in Source precedence. Unlike the
	// finite profile, a missing policy is established by these real reads.
	ResolvePolicy func() (*protocol.JSONObject, error)
}
type FileSystem interface{ ReadFile(string) ([]byte, error) }
type OSFileSystem struct{}

func (OSFileSystem) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

type Event map[string]any
type Options struct {
	Global        *cliglobalconfig.Service
	Settings      *clisettings.Service
	SettingsState *clisettings.State
	Environment   *Environment
	ConfigHome    string
	Host          Host
	FS            FileSystem
	Observe       func(Event)
	Diagnostics   *diagnostics.Process
	NowMillis     func() float64
	Plugins       *pluginregistry.Loader
	// MergeCcHahaEnvironment is the owning saved-provider producer. The scalar
	// legacy reader remains shared; this callback replaces only active-provider
	// resolution, never the trusted settings filters or assignment order.
	MergeCcHahaEnvironment func(protocol.JSONObject) (protocol.JSONObject, error)
}
type Service struct {
	options      Options
	ccdCaptured  bool
	ccdKeys      map[string]bool
	eligibility  *bool
	hostControls map[string]json.RawMessage
}

var frozenHostControls = []string{"HOME", "CLAUDE_CONFIG_DIR", "USER_TYPE", "CLAUDE_CODE_ENABLE_XAA", "CLAUDE_CODE_SIMPLE"}

func New(options Options) (*Service, error) {
	if options.Global == nil || options.Settings == nil || options.SettingsState == nil || options.Environment == nil || !filepath.IsAbs(options.ConfigHome) {
		return nil, errors.New("CLI startup env requires explicit enabled services, state, environment and config HOME")
	}
	h := options.Host
	if h.Profile == ResolvedPolicyReleaseProfile {
		if h.ResolvePolicy == nil || !options.Settings.UsesState(options.SettingsState) || (!h.PluginBaseAbsent && !options.Plugins.SharesSettingsState(options.SettingsState)) || (h.PluginBaseAbsent && options.Plugins != nil) {
			return nil, &CapabilityError{"native policy resolver/plugin host snapshot"}
		}
	} else if h.Profile != EmptyPolicyReleaseProfile || !h.RemoteCacheAbsent || !h.MDMCacheAbsent || !h.HKCUCacheAbsent || !h.PolicyFilesAbsent || !h.PluginBaseAbsent {
		return nil, &CapabilityError{"unresolved external policy/plugin host snapshot"}
	}
	for _, source := range h.AllowedSources {
		if !validSource(source) {
			return nil, &CapabilityError{"setting source " + source}
		}
	}
	if h.FlagPath != nil && !filepath.IsAbs(*h.FlagPath) {
		return nil, &CapabilityError{"unresolved flag settings path"}
	}
	if h.FlagInline != nil && (!json.Valid(h.FlagInline) || rawKind(h.FlagInline) != "object") {
		return nil, &CapabilityError{"inline settings non-object"}
	}
	if plugin := options.SettingsState.GetPluginSettingsBase(); plugin != nil && h.PluginBaseAbsent {
		return nil, &CapabilityError{"plugin cache contradicts absent host snapshot"}
	}
	if options.FS == nil {
		options.FS = OSFileSystem{}
	}
	if equalString(options.Environment.Get("USER_TYPE"), "ant") {
		return nil, &CapabilityError{"internal schema host profile"}
	}
	if options.NowMillis == nil {
		options.NowMillis = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	controls := map[string]json.RawMessage{}
	for _, key := range frozenHostControls {
		controls[key] = options.Environment.Get(key)
	}
	return &Service{options: options, hostControls: controls}, nil
}
func (s *Service) emit(event Event) {
	if s.options.Observe != nil {
		s.options.Observe(event)
	}
}
func fields(object protocol.JSONObject) []map[string]any {
	result := []map[string]any{}
	for _, property := range object.OwnProperties() {
		result = append(result, map[string]any{"key": string(property.NameJSON), "value": describe(property.Value)})
	}
	return result
}
func (s *Service) assign(object protocol.JSONObject) error {
	for _, property := range object.OwnProperties() {
		key := stringValue(property.NameJSON)
		if original, protected := s.hostControls[key]; protected && !scalarEqual(original, property.Value) {
			return &CapabilityError{"settings mutation of frozen filesystem/schema host " + key}
		}
		if key == "__proto__" || key == "constructor" || key == "prototype" {
			return &CapabilityError{"native environment prototype property"}
		}
		if err := s.options.Environment.SetProperty(property.NameJSON, property.Value); err != nil {
			return err
		}
	}
	s.emit(Event{"kind": "assign", "fields": fields(object)})
	return nil
}
func envFrom(settings *protocol.JSONObject) json.RawMessage {
	if settings == nil {
		return nil
	}
	return settings.Get("env")
}
func record(raw json.RawMessage) (protocol.JSONObject, error) {
	if rawKind(raw) == "object" {
		var object protocol.JSONObject
		err := object.UnmarshalJSON(raw)
		return object, err
	}
	if rawKind(raw) == "array" || rawKind(raw) == "string" && truthy(raw) {
		return protocol.JSONObject{}, &CapabilityError{"non-record legacy env container"}
	}
	return protocol.NewJSONObject(), nil
}
func (s *Service) filter(raw json.RawMessage) (protocol.JSONObject, error) {
	object, err := record(raw)
	if err != nil {
		return object, err
	}
	ssh := truthy(s.options.Environment.Get("ANTHROPIC_UNIX_SOCKET"))
	host, err := envTruthy(s.options.Environment.Get("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"))
	if err != nil {
		return object, err
	}
	result := protocol.NewJSONObject()
	for _, property := range object.OwnProperties() {
		key := stringValue(property.NameJSON)
		if ssh && sshKeys[key] {
			continue
		}
		if host && providerManagedKey(key) {
			continue
		}
		if key == "CC_HAHA_AGENT_TEAMS_ENABLED" || key == "CC_HAHA_AGENT_TEAMS_DEFAULT" {
			continue
		}
		if host && (key == "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST" || key == "CC_HAHA_LOCAL_ACCESS_TOKEN") {
			continue
		}
		if s.ccdKeys[string(property.NameJSON)] {
			continue
		}
		if err := result.SetPropertyJSON(property.NameJSON, property.Value); err != nil {
			return result, err
		}
	}
	return result, nil
}
func (s *Service) apply(raw json.RawMessage) error {
	object, err := s.filter(raw)
	if err != nil {
		return err
	}
	return s.assign(object)
}

// ApplySafeConfigEnvironmentVariables ports the complete original export for
// this explicit absent-policy/no-plugin/scalar environment host profile.
func (s *Service) ApplySafeConfigEnvironmentVariables() error {
	if err := s.checkHostControls(); err != nil {
		return err
	}
	if !s.ccdCaptured {
		s.ccdCaptured = true
		if equalString(s.options.Environment.Get("CLAUDE_CODE_ENTRYPOINT"), "claude-desktop") {
			s.ccdKeys = map[string]bool{}
			for _, property := range s.options.Environment.Snapshot().OwnProperties() {
				s.ccdKeys[string(property.NameJSON)] = true
			}
		}
	}
	global, err := s.options.Global.Get()
	if err != nil {
		return err
	}
	if err = s.apply(envFrom(global)); err != nil {
		return err
	}
	for _, source := range []string{"userSettings", "flagSettings"} {
		if !s.enabled(source) {
			continue
		}
		settings, e := s.getSource(source)
		if e != nil {
			return e
		}
		if e = s.apply(envFrom(settings)); e != nil {
			return e
		}
	}
	cc, err := s.ccHahaEnv()
	if err != nil {
		return err
	}
	if err = s.apply(cc); err != nil {
		return err
	}
	if err = s.computeEligibility(); err != nil {
		return err
	}
	policy, err := s.getSource("policySettings")
	if err != nil {
		return err
	}
	if err = s.apply(envFrom(policy)); err != nil {
		return err
	}
	merged, err := s.mergedEnv()
	if err != nil {
		return err
	}
	filtered, err := s.filter(merged)
	if err != nil {
		return err
	}
	for _, property := range filtered.OwnProperties() {
		if safeKeys[strings.ToUpper(stringValue(property.NameJSON))] {
			if err = s.options.Environment.SetProperty(property.NameJSON, property.Value); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Service) computeEligibility() error {
	if s.eligibility != nil {
		return nil
	}
	if s.options.Host.Profile == ResolvedPolicyReleaseProfile {
		// Native policy resolution owns provider/credential eligibility; the
		// finite fixture's synthetic remote-absence flag is not used here.
		return nil
	}
	if !equalString(s.options.Environment.Get("CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"), "1") {
		return &CapabilityError{"remote eligibility provider/auth path"}
	}
	value := false
	s.eligibility = &value
	return nil
}
func (s *Service) debug(raw json.RawMessage, level string) {
	s.emit(Event{"kind": "debug", "message": string(raw), "level": level})
}
func (s *Service) ApplyExtraCACertsFromConfig() error {
	if err := s.checkHostControls(); err != nil {
		return err
	}
	if truthy(s.options.Environment.Get("NODE_EXTRA_CA_CERTS")) {
		return nil
	}
	global, err := s.options.Global.Get()
	if err != nil {
		s.debug(concat(quote("CA certs: Config fallback failed: Error: "), quote(err.Error())), "error")
		return nil
	}
	user, err := s.getSource("userSettings")
	if err != nil {
		var cap *clisettings.CapabilityError
		if errors.As(err, &cap) {
			return err
		}
		var own *CapabilityError
		if errors.As(err, &own) {
			return err
		}
		s.debug(concat(quote("CA certs: Config fallback failed: Error: "), quote(err.Error())), "error")
		return nil
	}
	g, err := record(envFrom(global))
	if err != nil {
		return err
	}
	u, err := record(envFrom(user))
	if err != nil {
		return err
	}
	keys := func(raw json.RawMessage, object protocol.JSONObject) json.RawMessage {
		if !truthy(raw) {
			return quote("none")
		}
		units := []uint16{}
		for index, property := range object.OwnProperties() {
			if index > 0 {
				units = append(units, ',')
			}
			value, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
			units = append(units, value...)
		}
		return protocol.QuoteJavaScriptUTF16(units)
	}
	s.debug(concat(quote("CA certs: Config fallback - globalEnv keys: "), keys(envFrom(global), g), quote(", settingsEnv keys: "), keys(envFrom(user), u)), "debug")
	path := u.Get("NODE_EXTRA_CA_CERTS")
	if !truthy(path) {
		path = g.Get("NODE_EXTRA_CA_CERTS")
	}
	if !truthy(path) {
		return nil
	}
	if rawKind(path) == "object" || rawKind(path) == "array" {
		return &CapabilityError{"reference extra CA value"}
	}
	text, err := protocol.ExactJavaScriptStringJSON(path)
	if err != nil {
		return err
	}
	s.debug(concat(quote("CA certs: Found NODE_EXTRA_CA_CERTS in config/settings: "), text), "debug")
	if err = s.options.Environment.SetProperty(quote("NODE_EXTRA_CA_CERTS"), path); err != nil {
		return err
	}
	s.debug(concat(quote("CA certs: Applied NODE_EXTRA_CA_CERTS from config to process.env: "), text), "debug")
	return nil
}

func (s *Service) checkHostControls() error {
	for _, key := range frozenHostControls {
		if !scalarEqual(s.hostControls[key], s.options.Environment.Get(key)) {
			return &CapabilityError{"changed filesystem/schema host " + key}
		}
	}
	return nil
}
func (s *Service) ensureProvidersAbsent() error {
	_, err := s.options.FS.ReadFile(filepath.Join(s.options.ConfigHome, "cc-haha", "providers.json"))
	if err == nil || !os.IsNotExist(err) {
		return &CapabilityError{"cc-haha providers index/runtime"}
	}
	return nil
}
func (s *Service) ccHahaEnv() (json.RawMessage, error) {
	host, err := envTruthy(s.options.Environment.Get("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"))
	if err != nil {
		return nil, err
	}
	if !host {
		if s.options.MergeCcHahaEnvironment == nil {
			if err = s.ensureProvidersAbsent(); err != nil {
				return nil, err
			}
		}
	}
	raw, readErr := s.options.FS.ReadFile(filepath.Join(s.options.ConfigHome, "cc-haha", "settings.json"))
	raw = protocol.DecodeJavaScriptUTF8(raw)
	object := protocol.NewJSONObject()
	if readErr == nil && json.Valid(raw) && rawKind(raw) == "object" {
		_ = object.UnmarshalJSON(raw)
		value := object.Get("env")
		if value == nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			value = []byte("{}")
		}
		object, err = record(value)
		if err != nil {
			return nil, err
		}
		if truthy(object.Get("CC_HAHA_SEND_DISABLED_THINKING")) {
			base := object.Get("ANTHROPIC_BASE_URL")
			if truthy(base) && rawKind(base) != "string" {
				return nil, &CapabilityError{"DeepSeek baseURL typed includes"}
			}
			looks := strings.Contains(stringValue(base), "api.deepseek.com")
			for _, name := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL"} {
				model := object.Get(name)
				if truthy(model) {
					text, e := protocol.ExactJavaScriptStringJSON(model)
					if e != nil {
						return nil, e
					}
					value := strings.ToLower(stringValue(text))
					looks = looks || strings.HasPrefix(value, "deepseek-") || strings.HasPrefix(value, "deepseek_")
				}
			}
			if looks {
				_ = object.DeletePropertyJSON(quote("CC_HAHA_SEND_DISABLED_THINKING"))
				for _, name := range []string{"ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES"} {
					object.Set(name, quote("thinking,effort,adaptive_thinking,xhigh_effort,max_effort"))
				}
			}
		}
	}
	if s.options.MergeCcHahaEnvironment != nil {
		object, err = s.options.MergeCcHahaEnvironment(object)
		if err != nil {
			return nil, err
		}
	} else if err = s.ensureProvidersAbsent(); err != nil {
		return nil, err
	}
	return object.MarshalJSON()
}

// SharesSettings checks construction bindings without reading caches or files.
func (s *Service) SharesSettings(settings *clisettings.Service, state *clisettings.State, environment *Environment) bool {
	return s != nil && settings != nil && state != nil && environment != nil && s.options.Settings == settings && s.options.SettingsState == state && s.options.Environment == environment && settings.UsesState(state)
}
