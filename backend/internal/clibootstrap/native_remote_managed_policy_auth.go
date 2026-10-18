package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

// A ready source projection prevents getConfiguredApiKeyHelper's merged read
// from recursing into eligibility. It still includes its actual precedence,
// including a policy fallback while the remote leaf is not eligible yet.
func remoteManagedHelper(startup *clistartupenv.Service, n *NativeBoundary, bare bool) (json.RawMessage, error) {
	if bare {
		value, err := startup.GetSettingsForSource("flagSettings")
		if value == nil {
			return nil, err
		}
		return value.Get("apiKeyHelper"), err
	}
	var helper json.RawMessage
	for _, source := range startup.EnabledSettingSources() {
		var settings *protocol.JSONObject
		var err error
		if source == "policySettings" {
			settings, err = n.ResolvePolicy()
		} else {
			settings, err = startup.GetSettingsForSource(source)
		}
		if err != nil {
			return nil, err
		}
		if settings != nil {
			if value := settings.Get("apiKeyHelper"); value != nil {
				helper = value
			}
		}
	}
	return helper, nil
}

// Eligibility probes OAuth first and must not touch the API FD, helper
// projection, or legacy key when OAuth already wins. Fetch headers use the
// different Source API-key-first order, after the real OAuth freshness check.
func sampleRemoteManagedCredentials(eligibilityOnly bool, oauth func() (*protocol.JSONObject, error), apiKey func() (string, error)) (nativeManagedCredentials, error) {
	var tokens *protocol.JSONObject
	if eligibilityOnly {
		var err error
		tokens, err = oauth()
		if err != nil {
			return nativeManagedCredentials{}, err
		}
		if remoteManagedOAuthEligible(tokens) {
			return nativeManagedCredentials{OAuth: tokens}, nil
		}
	}
	key, err := apiKey()
	if err != nil {
		return nativeManagedCredentials{}, err
	}
	if key != "" {
		return nativeManagedCredentials{APIKey: key, OAuth: tokens}, nil
	}
	if !eligibilityOnly {
		tokens, err = oauth()
		if err != nil {
			return nativeManagedCredentials{}, err
		}
	}
	return nativeManagedCredentials{OAuth: tokens}, nil
}
func (m *MainPrint) sampleRemoteManagedCredentials(scope *cooperative.Scope, eligibilityOnly bool) (nativeManagedCredentials, error) {
	s := m.remoteManaged
	if s == nil || !scope.BelongsTo(m.Owner) || m.openAIAuth == nil {
		return nativeManagedCredentials{}, cooperative.ErrRestricted
	}
	owned, err := m.openAIAuth.owned(s.life, scope)
	if err != nil {
		return nativeManagedCredentials{}, err
	}
	env := m.nativeOpenAIEnvironment()
	bare := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	return sampleRemoteManagedCredentials(eligibilityOnly, func() (*protocol.JSONObject, error) {
		tokens, err := m.openAIAuth.claudeTokensFor(owned, env, bare)
		if err != nil && s.liveError(err) != nil {
			return nil, err
		}
		if tokens == nil {
			return nil, nil
		}
		copy := tokens.Object.Clone()
		return &copy, nil
	}, func() (string, error) {
		helper, err := remoteManagedHelper(m.Startup, m.options.Native, bare)
		if err != nil {
			if fatal := s.liveError(err); fatal != nil {
				return "", fatal
			}
			return "", nil
		}
		key, err := remoteManagedAPIKey(env, bare, !m.options.Interactive, helper, func() string {
			return m.openAIAuth.credentialFromDescriptor(env, "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", ".api_key")
		}, func() (bool, error) {
			global, err := m.Global.Get()
			if err != nil || global == nil {
				return false, err
			}
			return openaiauth.IsCustomAPIKeyApproved(*global, quoteMain(env["ANTHROPIC_API_KEY"]))
		}, func() (string, error) {
			err := m.openAIAuth.ensureLegacyAPIKeyCached(owned)
			return m.openAIAuth.legacyAPIKey, err
		})
		if err != nil && s.liveError(err) != nil {
			return "", err
		}
		return key, nil
	})
}
func (m *MainCommand) sampleRemoteManagedCredentials(scope *cooperative.Scope, eligibilityOnly bool) (nativeManagedCredentials, error) {
	s := m.remoteManaged
	if s == nil || !scope.BelongsTo(m.Owner) {
		return nativeManagedCredentials{}, cooperative.ErrRestricted
	}
	env := m.nativeCommandEnvironment()
	bare := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	return sampleRemoteManagedCredentials(eligibilityOnly, func() (*protocol.JSONObject, error) {
		tokens, err := m.nativeCommandClaudeTokens(s.life, scope)
		if err != nil && s.liveError(err) != nil {
			return nil, err
		}
		if tokens == nil {
			return nil, nil
		}
		copy := tokens.Object.Clone()
		return &copy, nil
	}, func() (string, error) {
		helper, err := remoteManagedHelper(m.Startup, m.Native, bare)
		if err != nil {
			if fatal := s.liveError(err); fatal != nil {
				return "", fatal
			}
			return "", nil
		}
		key, err := remoteManagedAPIKey(env, bare, m.NonInteractive, helper, func() string {
			return m.Native.credentialFromDescriptor(env, m.credentialPlatform, m.credentialDirectory, "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", ".api_key")
		}, func() (bool, error) {
			global, err := m.Global.Get()
			if err != nil || global == nil {
				return false, err
			}
			return openaiauth.IsCustomAPIKeyApproved(*global, quoteMain(env["ANTHROPIC_API_KEY"]))
		}, func() (string, error) { key, _, err := m.nativeCommandLegacyAPIKey(s.life, scope); return key, err })
		if err != nil && s.liveError(err) != nil {
			return "", err
		}
		return key, nil
	})
}

// skipRetrievingKeyFromApiKeyHelper is deliberately different from model
// auth: helper presence wins over legacy, but never supplies a cached helper
// value. Descriptor bytes and legacy memo come from the actual shared owners.
func remoteManagedAPIKey(env map[string]string, bare, preferThirdParty bool, helper json.RawMessage, readFD func() string, approved func() (bool, error), legacy func() (string, error)) (string, error) {
	key := env["ANTHROPIC_API_KEY"]
	if !bare && env["USER_TYPE"] == "ant" && envTruthyNative(env["COO_RUNNING_ON_HOMESPACE"]) {
		key = ""
	}
	if key != "" && (bare || preferThirdParty) {
		return key, nil
	}
	if bare {
		return "", nil
	}
	if envTruthyNative(env["CI"]) || env["NODE_ENV"] == "test" {
		if value := readFD(); value != "" {
			return value, nil
		}
		return key, nil
	}
	if key != "" {
		ok, err := approved()
		if err != nil {
			return "", err
		}
		if ok {
			return key, nil
		}
	}
	if value := readFD(); value != "" {
		return value, nil
	}
	if openaiauth.AuthTruthy(helper) {
		return "", nil
	}
	return legacy()
}

func newRemoteManagedPolicy(n *NativeBoundary, owner *cooperative.Owner) *nativeRemoteManagedPolicy {
	life, cancel := context.WithCancel(context.Background())
	failure, failSecurity := context.WithCancelCause(context.Background())
	return &nativeRemoteManagedPolicy{failureContext: failure, failSecurity: failSecurity, n: n, owner: owner, life: life, cancel: cancel, consents: map[string]*nativeManagedConsent{}, listeners: map[uint64]func(NativeManagedPolicyConsentEvent){}, cacheListeners: map[uint64]func(){}}
}
func (m *MainPrint) attachRemoteManagedPolicy() error {
	if parent := m.nativeHelperParent(); parent != nil && parent.remoteManaged != nil {
		s := parent.remoteManaged
		m.remoteManaged = s
		s.mu.Lock()
		s.cacheListenerSequence++
		id := s.cacheListenerSequence
		s.cacheListeners[id] = func() { m.settingState.ResetSettingsCache() }
		s.mu.Unlock()
		m.removeRemoteManagedCacheListener = func() { s.mu.Lock(); delete(s.cacheListeners, id); s.mu.Unlock() }
		return nil
	}
	s := newRemoteManagedPolicy(m.options.Native, m.Owner)
	s.sample = m.sampleRemoteManagedCredentials
	s.interactive = m.options.Interactive
	s.configure = m.configureRemoteManagedPolicy
	s.invalidate = func() { m.settingState.ResetSettingsCache() }
	s.notify = func(scope *cooperative.Scope) error {
		if m.initialized && m.Host != nil {
			return m.nativeSettingsChangedOnOwner(s.life, scope, "policySettings", "")
		}
		m.settingState.ResetSettingsCache()
		return nil
	}
	m.remoteManaged = s
	m.options.Native.mu.Lock()
	m.options.Native.remoteManaged = s
	m.options.Native.mu.Unlock()
	return nil
}
func (m *MainCommand) attachRemoteManagedPolicy() error {
	s := newRemoteManagedPolicy(m.Native, m.Owner)
	s.sample = m.sampleRemoteManagedCredentials
	s.interactive = !m.NonInteractive
	s.configure = m.configureRemoteManagedPolicy
	s.commandDialog = func(ctx context.Context, id string, names []string) (bool, error) {
		return tui.PromptManagedSettings(ctx, m.managedPolicyInput, m.Output, id, names)
	}
	s.invalidate = func() {
		if m.Settings.settingsState != nil {
			m.Settings.settingsState.ResetSettingsCache()
		}
	}
	s.notify = func(*cooperative.Scope) error { m.Settings.InvalidateSettingsCache(); return nil }
	m.remoteManaged = s
	m.Native.mu.Lock()
	m.Native.remoteManaged = s
	m.Native.mu.Unlock()
	return nil
}
func (m *MainPrint) prepareRemoteManagedPolicy() error {
	if m.remoteManaged == nil || m.remoteManaged.owner != m.Owner {
		return nil
	}
	return m.remoteManaged.prepareForInit()
}
func (m *MainCommand) prepareRemoteManagedPolicy() error {
	if m.remoteManaged == nil {
		return nil
	}
	// The command path formerly copied process env only after the safe pass.
	// Eligibility must see each already-applied trusted non-policy assignment.
	for key, value := range m.nativeCommandEnvironment() {
		m.Native.Environment[key] = value
	}
	return m.remoteManaged.prepareForInit()
}
func (s *nativeRemoteManagedPolicy) prepareForInit() error {
	s.mu.Lock()
	known := s.eligible != nil
	s.mu.Unlock()
	if known {
		return nil
	}
	return s.owner.Run(s.life, func(scope *cooperative.Scope) error {
		if err := s.prepare(scope); err != nil {
			return err
		}
		s.mu.Lock()
		eligible := s.eligible != nil && *s.eligible
		s.mu.Unlock()
		if eligible {
			return s.initializeLoading(scope)
		}
		return nil
	})
}

func (s *nativeRemoteManagedPolicy) configureFetch(command *MainCommand, read func(context.Context, *cooperative.Scope, bool) (*openaiauth.Value, error), invalidate func()) error {
	env := s.n.nativeEnvironmentSnapshot()
	config, err := desktopoauth.ResolveConfig(env)
	if err != nil {
		return err
	}
	tls, err := tlsconfig.NewState(s.n.readFile)
	if err != nil {
		return err
	}
	client, err := webfetch.NewHTTPClient(env, tls)
	if err != nil {
		return err
	}
	client.CheckRedirect = remoteManagedRedirect
	run := claudeMCPCredentialRun(s.owner)
	reader := func(ctx context.Context, clear bool) (*openaiauth.Value, error) {
		var value *openaiauth.Value
		err := run(ctx, func(scope *cooperative.Scope) error { var err error; value, err = read(ctx, scope, clear); return err })
		return value, err
	}
	producer := &cliauth.OAuthClient{Config: config, HTTP: client, NowMillis: s.n.NowMillis, Environment: func() map[string]string { return env }}
	producer.Settings = func(ctx context.Context) (protocol.JSONObject, error) {
		result := protocol.NewJSONObject()
		err := run(ctx, func(*cooperative.Scope) error {
			value, e := command.Startup.GetInitialSettings()
			if value != nil {
				result = value.Clone()
			}
			return e
		})
		return result, err
	}
	producer.PreviousClaude = func(ctx context.Context) (protocol.JSONObject, protocol.JSONObject, error) {
		global, tokens := protocol.NewJSONObject(), protocol.NewJSONObject()
		err := run(ctx, func(scope *cooperative.Scope) error {
			current, e := command.Global.Get()
			if e != nil {
				return e
			}
			if current != nil {
				global = current.Clone()
			}
			value, e := read(ctx, scope, false)
			if value != nil {
				tokens = value.Object.Clone()
			}
			return e
		})
		return global, tokens, err
	}
	producer.UpdateClaudeProfile = func(ctx context.Context, profile protocol.JSONObject) error {
		return run(ctx, func(*cooperative.Scope) error { return command.updateCLIRefreshProfile(profile) })
	}
	life, cancel := context.WithCancel(s.life)
	refresh := &nativeClaudeMCPRefresh{command: command, run: run, read: reader, client: producer, lifetime: life, cancel: cancel, invalidateMemo: invalidate}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		client.CloseIdleConnections()
		return cooperative.ErrClosed
	}
	s.client, s.endpoint, s.refresh = client, strings.TrimSuffix(config.BaseAPIURL, "/")+"/api/claude_code/settings", refresh
	s.mu.Unlock()
	return nil
}
func (m *MainPrint) configureRemoteManagedPolicy(scope *cooperative.Scope) error {
	s := m.remoteManaged
	if err := s.check(scope); err != nil {
		return err
	}
	if s.configured() {
		return nil
	}
	command := &MainCommand{Owner: m.Owner, Native: m.options.Native, Settings: m.nativeSettings, Startup: m.Startup, Global: m.Global, Secure: m.secure, mcpClaudeAuth: m.openAIAuth, credentialPlatform: m.options.Platform}
	return s.captureConfiguration(s.configureFetch(command, func(ctx context.Context, next *cooperative.Scope, clear bool) (*openaiauth.Value, error) {
		if clear {
			m.openAIAuth.claudeTokens = nil
			m.openAIAuth.claudeTokensLoaded = false
			m.secure.Clear()
		}
		return m.openAIAuth.claudeTokensFor(ctx, m.nativeOpenAIEnvironment(), envTruthyNative(m.nativeOpenAIEnvironment()["CLAUDE_CODE_SIMPLE"]))
	}, func() { m.openAIAuth.claudeTokens = nil; m.openAIAuth.claudeTokensLoaded = false }))
}
func (m *MainCommand) configureRemoteManagedPolicy(scope *cooperative.Scope) error {
	s := m.remoteManaged
	if err := s.check(scope); err != nil {
		return err
	}
	if s.configured() {
		return nil
	}
	return s.captureConfiguration(s.configureFetch(m, func(ctx context.Context, next *cooperative.Scope, clear bool) (*openaiauth.Value, error) {
		if clear {
			m.commandClaudeTokens = nil
			m.Secure.Clear()
		}
		return m.nativeCommandClaudeTokens(ctx, next)
	}, func() { m.commandClaudeTokens = nil }))
}
func (m *MainPrint) beginRemoteManagedPolicy(ctx context.Context) error {
	s := m.remoteManaged
	if s == nil || s.owner != m.Owner {
		return nil
	}
	return m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		s.mu.Lock()
		eligible := s.eligible != nil && *s.eligible
		s.mu.Unlock()
		if eligible {
			if err := s.configure(scope); err != nil {
				return err
			}
		}
		return s.begin(scope)
	})
}
func (m *MainCommand) beginRemoteManagedPolicy(ctx context.Context) error {
	s := m.remoteManaged
	if s == nil {
		return nil
	}
	return m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		s.mu.Lock()
		eligible := s.eligible != nil && *s.eligible
		s.mu.Unlock()
		if eligible {
			if err := s.configure(scope); err != nil {
				return err
			}
		}
		return s.begin(scope)
	})
}

// Refresh is the actual post-interactive-login/onboarding consumer, distinct
// from CLI installOAuthTokens, whose Source contract only clears the cache.
func (m *MainPrint) RefreshRemoteManagedPolicy(ctx context.Context) error {
	s := m.remoteManaged
	if s == nil || s.owner != m.Owner {
		return cooperative.ErrRestricted
	}
	return m.Owner.Run(ctx, func(scope *cooperative.Scope) error { return s.refreshAfterAuth(scope) })
}

func (s *nativeRemoteManagedPolicy) configured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client != nil || s.configurationError != nil
}
func (s *nativeRemoteManagedPolicy) captureConfiguration(err error) error {
	if err == nil {
		return nil
	}
	if fatal := s.liveError(err); fatal != nil {
		return fatal
	}
	s.mu.Lock()
	s.configurationError = err
	s.mu.Unlock()
	return nil
}
