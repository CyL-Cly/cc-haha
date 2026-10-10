package desktopoauth

// Source custom OAuth deployments retain the environment's MCP proxy address.
func ResolveMCPProxy(environment map[string]string) (string, string) {
	if environment["USER_TYPE"] == "ant" && envTruthy(environment["USE_LOCAL_OAUTH"]) {
		return "http://localhost:8205", "/v1/toolbox/shttp/mcp/{server_id}"
	}
	if environment["USER_TYPE"] == "ant" && envTruthy(environment["USE_STAGING_OAUTH"]) {
		return "https://mcp-proxy-staging.anthropic.com", "/v1/mcp/{server_id}"
	}
	return "https://mcp-proxy.anthropic.com", "/v1/mcp/{server_id}"
}
