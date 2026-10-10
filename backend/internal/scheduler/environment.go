package scheduler

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func cloneEnvironment(env map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range env {
		out[key] = value
	}
	return out
}
func taskProviderID(task protocol.JSONObject) (string, bool, bool, error) {
	value := task.Get("providerId")
	if value == nil {
		return "", false, false, nil
	}
	if string(value) == "null" {
		return "", true, true, nil
	}
	var id string
	if json.Unmarshal(value, &id) != nil {
		return "", true, false, nil
	}
	return id, true, false, nil
}
func desktopSettingsEnv(o Options) protocol.JSONObject {
	data, e := o.Files.ReadFile(filepath.Join(o.ConfigDir, "cc-haha/settings.json"))
	if e != nil {
		return protocol.NewJSONObject()
	}
	var settings, env protocol.JSONObject
	if settings.UnmarshalJSON(data) != nil || env.UnmarshalJSON(settings.Get("env")) != nil {
		return protocol.NewJSONObject()
	}
	return env
}
func stripInheritedProvider(o Options, task protocol.JSONObject) (bool, error) {
	_, present, _, e := taskProviderID(task)
	if e != nil {
		return false, e
	}
	if present {
		return true, nil
	}
	if _, e = o.Files.Stat(filepath.Join(o.ConfigDir, "cc-haha/providers.json")); e == nil {
		return true, nil
	}
	env := desktopSettingsEnv(o)
	for _, property := range env.OwnProperties() {
		var name, value string
		if json.Unmarshal(property.NameJSON, &name) == nil && json.Unmarshal(property.Value, &value) == nil && clistartupenv.IsProviderManagedEnvVar(name) && jsTrim(value) != "" {
			return true, nil
		}
	}
	return false, nil
}
func markManagedOAuth(o Options, task protocol.JSONObject) (bool, error) {
	_, present, official, e := taskProviderID(task)
	if e != nil {
		return false, e
	}
	if official {
		return true, nil
	}
	if present && len(task.Get("providerId")) > 0 && task.Get("providerId")[0] == '"' {
		return false, nil
	}
	env := desktopSettingsEnv(o)
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		var value string
		if json.Unmarshal(env.Get(key), &value) == nil && jsTrim(value) != "" {
			return false, nil
		}
	}
	return true, nil
}

// BuildTaskEnvironment ports the real cron child-env precedence independently
// of the desktop's active-provider selection. Missing, null and string IDs
// remain distinct; the Executor receives this fully resolved launch authority.
func BuildTaskEnvironment(ctx context.Context, o Options, task protocol.JSONObject, workDir string) (map[string]string, error) {
	if o.GetProcessEnvironment == nil || o.ProviderRuntimeEnv == nil || o.NetworkEnvironment == nil || o.AgentTeamsEnabled == nil || o.OfficialOAuthEnv == nil || o.Files == nil || o.LogError == nil {
		return nil, &CapabilityError{"actual scheduled shell/provider/network/settings/OAuth producers"}
	}
	clean, e := o.GetProcessEnvironment(ctx)
	if e != nil {
		return nil, e
	}
	clean = cloneEnvironment(clean)
	delete(clean, "CLAUDE_CODE_OAUTH_TOKEN")
	delete(clean, "CC_HAHA_AGENT_TEAMS_ENABLED")
	strip, e := stripInheritedProvider(o, task)
	if e != nil {
		return nil, e
	}
	if strip {
		for key := range clean {
			if clistartupenv.IsProviderManagedEnvVar(key) {
				delete(clean, key)
			}
		}
	}
	id, present, official, e := taskProviderID(task)
	if e != nil {
		return nil, e
	}
	var provider map[string]string
	if present && !official && len(task.Get("providerId")) > 0 && task.Get("providerId")[0] == '"' {
		provider, e = o.ProviderRuntimeEnv(ctx, id)
		if e != nil {
			return nil, e
		}
		if provider != nil {
			provider = cloneEnvironment(provider)
		}
	}
	model := ""
	if value := task.Get("model"); value != nil && string(value) != "null" {
		if json.Unmarshal(value, &model) != nil {
			return nil, malformed("model.trim")
		}
		model = jsTrim(model)
	}
	if provider != nil && model != "" {
		provider["ANTHROPIC_MODEL"] = model
	}
	attributionModel := model
	if attributionModel == "" {
		attributionModel = provider["ANTHROPIC_MODEL"]
	}
	if attributionModel == "" {
		attributionModel = clean["ANTHROPIC_MODEL"]
	}
	network, e := o.NetworkEnvironment(ctx, cloneEnvironment(clean))
	if e != nil {
		return nil, e
	}
	teams, e := o.AgentTeamsEnabled(ctx)
	if e != nil {
		return nil, e
	}
	out := cloneEnvironment(clean)
	out["CLAUDE_CODE_ENABLE_TASKS"] = "1"
	out["CC_HAHA_AGENT_TEAMS_ENABLED"] = "0"
	if teams {
		out["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	}
	out["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	out["CALLER_DIR"] = workDir
	out["PWD"] = workDir
	out["CC_HAHA_SKIP_DOTENV"] = "1"
	if provider != nil {
		out["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"] = "1"
		for key, value := range provider {
			out[key] = value
		}
	}
	managed, e := markManagedOAuth(o, task)
	if e != nil {
		return nil, e
	}
	if managed {
		out["CLAUDE_CODE_ENTRYPOINT"] = "claude-desktop"
		env, e := o.OfficialOAuthEnv(ctx)
		if e != nil {
			o.LogError(e)
		} else {
			for key, value := range env {
				out[key] = value
			}
		}
	}
	for key, value := range network {
		out[key] = value
	}
	if normalized := jsTrim(attributionModel); normalized != "" {
		out["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0"
		if strings.HasPrefix(strings.ToLower(normalized), "claude") {
			out["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "1"
		}
	}
	return out, nil
}
