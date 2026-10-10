package analytics

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type RecordInput struct {
	Event      Event
	ID, UserID string
	Timestamp  time.Time
	Core, User protocol.JSONObject
}

func jsTruthy(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" || string(raw) == `""` {
		return false
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil && raw[0] != '"' {
		return n != 0
	}
	return true
}
func base64JSON(raw json.RawMessage) json.RawMessage {
	return scalar(base64.StdEncoding.EncodeToString(protocol.StringifyValidatedJavaScriptJSON(raw)))
}

// Record mirrors the internal-event branch of transformLogsToEvents and the
// proto's sparse JSON serializer. Runtime facts enter through Core; this does
// not invent Node/V8 process counters for a Go process.
func Record(input RecordInput) (protocol.JSONObject, error) {
	event := protocol.NewJSONObject(field("event_name", input.Event.Name), field("client_timestamp", input.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")))
	coreKeys := []struct {
		in, out  string
		required bool
	}{
		{"model", "model", true}, {"sessionId", "session_id", true}, {"userType", "user_type", true}, {"betas", "betas", false}, {"entrypoint", "entrypoint", false}, {"agentSdkVersion", "agent_sdk_version", false},
		{"clientType", "client_type", true}, {"sweBenchRunId", "swe_bench_run_id", false}, {"sweBenchInstanceId", "swe_bench_instance_id", false}, {"sweBenchTaskId", "swe_bench_task_id", false},
		{"agentId", "agent_id", false}, {"parentSessionId", "parent_session_id", false}, {"agentType", "agent_type", false}, {"teamName", "team_name", false},
	}
	for _, key := range coreKeys {
		if value := input.Core.Get(key.in); value != nil && (key.required || jsTruthy(value)) {
			event.Set(key.out, value)
		}
	}
	event.Set("is_interactive", scalar(input.Core.String("isInteractive") == "true"))
	var envContext protocol.JSONObject
	_ = json.Unmarshal(input.Core.Get("envContext"), &envContext)
	env := protocol.NewJSONObject()
	for _, key := range []struct {
		in, out  string
		optional bool
	}{
		{"platform", "platform", false}, {"platformRaw", "platform_raw", false}, {"arch", "arch", false}, {"nodeVersion", "node_version", false}, {"terminal", "terminal", false}, {"packageManagers", "package_managers", false}, {"runtimes", "runtimes", false},
		{"isRunningWithBun", "is_running_with_bun", false}, {"isCi", "is_ci", false}, {"isClaubbit", "is_claubbit", false}, {"isClaudeCodeRemote", "is_claude_code_remote", false}, {"isLocalAgentMode", "is_local_agent_mode", false}, {"isConductor", "is_conductor", false},
		{"isGithubAction", "is_github_action", false}, {"isClaudeCodeAction", "is_claude_code_action", false}, {"isClaudeAiAuth", "is_claude_ai_auth", false}, {"version", "version", false}, {"buildTime", "build_time", false}, {"deploymentEnvironment", "deployment_environment", false},
		{"remoteEnvironmentType", "remote_environment_type", true}, {"claudeCodeContainerId", "claude_code_container_id", true}, {"claudeCodeRemoteSessionId", "claude_code_remote_session_id", true}, {"githubEventName", "github_event_name", true}, {"githubActionsRunnerEnvironment", "github_actions_runner_environment", true}, {"githubActionsRunnerOs", "github_actions_runner_os", true}, {"githubActionRef", "github_action_ref", true}, {"wslVersion", "wsl_version", true}, {"linuxDistroId", "linux_distro_id", true}, {"linuxDistroVersion", "linux_distro_version", true}, {"linuxKernel", "linux_kernel", true}, {"vcs", "vcs", true}, {"versionBase", "version_base", true},
	} {
		if value := envContext.Get(key.in); value != nil && (!key.optional || jsTruthy(value)) {
			env.Set(key.out, value)
		}
	}
	if !jsTruthy(env.Get("terminal")) {
		env.Set("terminal", scalar("unknown"))
	}
	if envContext.Get("tags") != nil {
		var tags []string
		for _, value := range splitTags(envContext.String("tags")) {
			if value != "" {
				tags = append(tags, value)
			}
		}
		if len(tags) > 0 {
			env.Set("tags", scalar(tags))
		}
	}
	if raw := input.User.Get("githubActionsMetadata"); jsTruthy(raw) {
		var gh protocol.JSONObject
		_ = json.Unmarshal(raw, &gh)
		mapped := protocol.NewJSONObject()
		for _, key := range []struct{ in, out string }{{"actorId", "actor_id"}, {"repositoryId", "repository_id"}, {"repositoryOwnerId", "repository_owner_id"}} {
			if value := gh.Get(key.in); value != nil {
				mapped.Set(key.out, value)
			}
		}
		value, _ := mapped.MarshalJSON()
		env.Set("github_actions_metadata", value)
	}
	envRaw, _ := env.MarshalJSON()
	event.Set("env", envRaw)
	if value := input.Core.Get("processMetrics"); jsTruthy(value) {
		event.Set("process", base64JSON(value))
	}
	additional := protocol.NewJSONObject()
	if value := input.Core.Get("rh"); jsTruthy(value) {
		additional.Set("rh", value)
	}
	if jsTruthy(input.Core.Get("kairosActive")) {
		additional.Set("is_assistant_mode", scalar(true))
	}
	for _, key := range []struct{ in, out string }{{"skillMode", "skill_mode"}, {"observerMode", "observer_mode"}} {
		if value := input.Core.Get(key.in); jsTruthy(value) {
			additional.Set(key.out, value)
		}
	}
	additional.Assign(input.Event.Metadata)
	for _, key := range []struct{ in, out string }{{"_PROTO_skill_name", "skill_name"}, {"_PROTO_plugin_name", "plugin_name"}, {"_PROTO_marketplace_name", "marketplace_name"}} {
		if value := additional.Get(key.in); len(value) > 0 && value[0] == '"' {
			event.Set(key.out, value)
		}
	}
	additional = StripProtoFields(additional)
	if len(additional.OwnProperties()) > 0 {
		value, _ := additional.MarshalJSON()
		event.Set("additional_metadata", base64JSON(value))
	}
	auth := protocol.NewJSONObject()
	for _, key := range []struct{ in, out string }{{"accountUuid", "account_uuid"}, {"organizationUuid", "organization_uuid"}} {
		if value := input.User.Get(key.in); jsTruthy(value) {
			auth.Set(key.out, value)
		}
	}
	if len(auth.OwnProperties()) > 0 {
		value, _ := auth.MarshalJSON()
		event.Set("auth", value)
	}
	if value := input.User.Get("email"); value != nil {
		event.Set("email", value)
	}
	event.Set("event_id", scalar(input.ID))
	if input.UserID != "" {
		event.Set("device_id", scalar(input.UserID))
	}
	value, e := event.MarshalJSON()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return protocol.NewJSONObject(field("event_type", "ClaudeCodeInternalEvent"), protocol.JSONField{Name: "event_data", Value: value}), nil
}

func splitTags(value string) []string {
	values := strings.Split(value, ",")
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return values
}
