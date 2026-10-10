package application

import (
	"context"
	"path/filepath"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// These are the original SessionStartOptions values, before CLI aliases and
// provider defaults are resolved. A nil provider selects the official label.
type desktopTeamLaunch struct {
	provider      *string
	model, effort string
}

func desktopTeamEnvironment(ctx context.Context, o Options, environment map[string]string) (map[string]string, error) {
	if o.desktopTeamLaunch == nil {
		return environment, nil
	}
	getter := o.DesktopTeamsEnabled
	if getter == nil && o.ProviderOptions != nil && o.ProviderOptions.Settings != nil {
		getter = o.ProviderOptions.Settings.GetAgentTeamsEnabled
	}
	if getter == nil {
		// Direct DesktopFactory users still use the real settings/migration
		// producers; they do not inherit the server's preference as a constant.
		store := o.Store
		if store == nil {
			store = storage.New(storage.Options{})
		}
		upgrader, err := migrations.New(migrations.Options{Store: store})
		if err != nil {
			return nil, err
		}
		home := environment["HOME"]
		if runtime.GOOS == "windows" && environment["USERPROFILE"] != "" {
			home = environment["USERPROFILE"]
		}
		config := environment["CLAUDE_CONFIG_DIR"]
		if config == "" {
			config = filepath.Join(home, ".claude")
		}
		service, err := settings.New(settings.Options{Store: store, HomeDir: home, ConfigDir: config, ProjectRoot: o.WorkDir, Environment: func() map[string]string { return environment }, EnsureMigrated: upgrader.SettingsCallback, ShellEnvironment: newTerminalEnvironment(environment).Get, GitExecutable: "git"})
		if err != nil {
			return nil, err
		}
		getter = service.GetAgentTeamsEnabled
	}
	enabled, err := getter(ctx)
	if err != nil {
		return nil, err
	}
	next := make(map[string]string, len(environment)+4)
	for key, value := range environment {
		next[key] = value
	}
	next["CLAUDE_CODE_ENABLE_TASKS"] = "1"
	next["CC_HAHA_AGENT_TEAMS_ENABLED"] = "0"
	if enabled {
		next["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	}
	next["CC_HAHA_TEAM_REVIEW_REQUIRED"] = "1"
	provider := "claude-official"
	if o.desktopTeamLaunch.provider != nil {
		provider = *o.desktopTeamLaunch.provider
	}
	model := o.desktopTeamLaunch.model
	if model == "" {
		model = environment["ANTHROPIC_MODEL"]
	}
	if model == "" {
		model = "default"
	}
	leader := protocol.NewJSONObject(protocol.JSONField{Name: "providerId", Value: raw(provider)}, protocol.JSONField{Name: "modelId", Value: raw(model)})
	if o.desktopTeamLaunch.effort != "" {
		leader.Set("effortLevel", raw(o.desktopTeamLaunch.effort))
	}
	next["CC_HAHA_TEAM_LEADER_RUNTIME"] = string(raw(leader))
	return next, nil
}
