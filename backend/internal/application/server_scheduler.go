package application

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/networksettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/scheduler"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func newDesktopScheduler(o ServerOptions, base Options, host func() *server.Server, shell *terminalEnvironment, editable *settings.Service, managed *settings.ManagedService, store *storage.Store, oauth *desktopoauth.Service, diagnostics *diagnosticsapi.Service) (*scheduler.Service, error) {
	base.OnCanonicalTranscriptMutation = func(path string) error { host().NotifyCanonicalTranscriptMutation(path); return nil }
	adapters, err := adapterconfig.New(adapterconfig.Options{ConfigDir: o.ConfigDir, ReadFile: os.ReadFile, LogError: func(e error) { _, _ = fmt.Fprintf(o.Stderr, "[AdapterService] %v\n", e) }})
	if err != nil {
		return nil, err
	}
	return scheduler.New(scheduler.Options{GetRawAdapterConfig: adapters.GetRawConfig, Home: o.HomeDir, ConfigDir: o.ConfigDir, Environment: func() map[string]string {
		env := make(map[string]string, len(o.Environment))
		for k, v := range o.Environment {
			env[k] = v
		}
		return env
	}, Now: time.Now, RandomBytes: func(n int) ([]byte, error) { b := make([]byte, n); _, e := rand.Read(b); return b, e }, Execute: scheduledExecutor(base), GetProcessEnvironment: shell.Get, ProviderRuntimeEnv: func(ctx context.Context, id string) (map[string]string, error) {
		providers, e := providerconfig.New(providerconfig.Options{Store: store, ConfigDir: o.ConfigDir, Settings: editable, Managed: managed})
		if e != nil {
			return nil, e
		}
		provider, e := providers.Provider(ctx, id)
		if e != nil {
			return nil, e
		}
		return providerconfig.BuildManagedEnv(provider, providerconfig.ManagedEnvironmentOptions{ServerPort: o.Port, ProxyReady: true, ProxyPath: "/proxy/providers/" + id})
	}, NetworkEnvironment: func(ctx context.Context, env map[string]string) (map[string]string, error) {
		user, e := editable.GetUserSettings(ctx)
		if e != nil {
			return nil, e
		}
		raw, e := json.Marshal(user)
		if e != nil {
			return nil, e
		}
		return networksettings.BuildEnvironment(networksettings.Normalize(raw), env)
	}, AgentTeamsEnabled: editable.GetAgentTeamsEnabled, OfficialOAuthEnv: func(ctx context.Context) (map[string]string, error) {
		token, e := oauth.EnsureFreshAccessToken(ctx)
		if e != nil {
			return nil, e
		}
		env := map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}
		if token != nil && *token != "" {
			env["CLAUDE_CODE_OAUTH_TOKEN"] = *token
		}
		return env, nil
	}, CreateSession: func(ctx context.Context, workDir, mode string) (string, error) {
		return host().CreateScheduledSession(ctx, workDir, mode)
	}, DeletePlaceholder: func(ctx context.Context, id string) error { return host().DeleteScheduledPlaceholder(ctx, id) }, AppendSessionMetadata: func(ctx context.Context, id string, m protocol.JSONObject) error {
		return host().AppendScheduledSessionMetadata(ctx, id, m)
	}, RecordDiagnostic: func(_ context.Context, value protocol.JSONObject) {
		_, e := diagnostics.Record(diagnosticsapi.Input{Type: value.String("type"), Severity: value.String("severity"), Summary: value.String("summary"), SessionID: value.String("sessionId"), Details: value.Get("details")})
		if e != nil {
			_, _ = fmt.Fprintf(o.Stderr, "[CronScheduler] %v\n", e)
		}
	}, LogError: func(e error) { _, _ = fmt.Fprintf(o.Stderr, "[CronScheduler] %v\n", e) }})
}
