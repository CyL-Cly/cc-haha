package ccrsessions

import "strings"

func RemoteSessionURL(env map[string]string, sid string) string {
	origin := "https://claude.ai"
	if env["USER_TYPE"] == "ant" && truthy(env["USE_LOCAL_OAUTH"]) {
		origin = env["CLAUDE_LOCAL_OAUTH_APPS_BASE"]
		if origin == "" {
			origin = "http://localhost:4000"
		}
	} else if env["USER_TYPE"] == "ant" && truthy(env["USE_STAGING_OAUTH"]) {
		origin = "https://claude-ai.staging.ant.dev"
	}
	if custom := env["CLAUDE_CODE_CUSTOM_OAUTH_URL"]; custom != "" {
		origin = custom
	}
	return strings.TrimRight(origin, "/") + "/code/" + sid
}
