package lspruntime

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// SubprocessEnvironment is Source subprocessEnv's LSP branch. Explicit server
// env is applied afterwards, just as child_process.spawn's original spread.
func SubprocessEnvironment(environment map[string]string) map[string]string {
	copy := map[string]string{}
	for k, v := range environment {
		copy[k] = v
	}
	delete(copy, "CC_HAHA_LOCAL_ACCESS_TOKEN")
	truth := func(v string) bool {
		switch protocol.TrimJavaScriptString(strings.ToLower(v)) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	}
	if truth(environment["CLAUDE_CODE_SUBPROCESS_ENV_SCRUB"]) {
		for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_FOUNDRY_API_KEY", "ANTHROPIC_CUSTOM_HEADERS", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK", "GOOGLE_APPLICATION_CREDENTIALS", "AZURE_CLIENT_SECRET", "AZURE_CLIENT_CERTIFICATE_PATH", "ACTIONS_ID_TOKEN_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL", "ACTIONS_RUNTIME_TOKEN", "ACTIONS_RUNTIME_URL", "ALL_INPUTS", "OVERRIDE_GITHUB_TOKEN", "DEFAULT_WORKFLOW_TOKEN", "SSH_SIGNING_KEY"} {
			delete(copy, key)
			delete(copy, "INPUT_"+key)
		}
	}
	return copy
}
