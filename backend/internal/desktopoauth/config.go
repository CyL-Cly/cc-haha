package desktopoauth

import (
	"errors"
	"net/url"
	"strings"
)

type Config struct{ BaseAPIURL, ConsoleAuthorizeURL, ClaudeAuthorizeURL, TokenURL, ClientID, ManualRedirectURL, FileSuffix string }

var ClaudeScopes = []string{"user:profile", "user:inference", "user:sessions:claude_code", "user:mcp_servers", "user:file_upload"}
var AllScopes = []string{"org:create_api_key", "user:profile", "user:inference", "user:sessions:claude_code", "user:mcp_servers", "user:file_upload"}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func ResolveConfig(env map[string]string) (Config, error) {
	c := Config{BaseAPIURL: "https://api.anthropic.com", ConsoleAuthorizeURL: "https://platform.claude.com/oauth/authorize", ClaudeAuthorizeURL: "https://claude.com/cai/oauth/authorize", TokenURL: "https://platform.claude.com/v1/oauth/token", ClientID: "9d1c250a-e61b-44d9-88ed-5944d1962f5e", ManualRedirectURL: "https://platform.claude.com/oauth/code/callback"}
	if env["USER_TYPE"] == "ant" && envTruthy(env["USE_LOCAL_OAUTH"]) {
		base, apps, console := "http://localhost:8000", "http://localhost:4000", "http://localhost:3000"
		if v, ok := env["CLAUDE_LOCAL_OAUTH_API_BASE"]; ok {
			base = strings.TrimSuffix(v, "/")
		}
		if v, ok := env["CLAUDE_LOCAL_OAUTH_APPS_BASE"]; ok {
			apps = strings.TrimSuffix(v, "/")
		}
		if v, ok := env["CLAUDE_LOCAL_OAUTH_CONSOLE_BASE"]; ok {
			console = strings.TrimSuffix(v, "/")
		}
		c = Config{BaseAPIURL: base, ConsoleAuthorizeURL: console + "/oauth/authorize", ClaudeAuthorizeURL: apps + "/oauth/authorize", TokenURL: base + "/v1/oauth/token", ClientID: "22422756-60c9-4084-8eb7-27705fd5cf9a", ManualRedirectURL: console + "/oauth/code/callback", FileSuffix: "-local-oauth"}
	} else if env["USER_TYPE"] == "ant" && envTruthy(env["USE_STAGING_OAUTH"]) {
		c = Config{BaseAPIURL: "https://api-staging.anthropic.com", ConsoleAuthorizeURL: "https://platform.staging.ant.dev/oauth/authorize", ClaudeAuthorizeURL: "https://claude-ai.staging.ant.dev/oauth/authorize", TokenURL: "https://platform.staging.ant.dev/v1/oauth/token", ClientID: "22422756-60c9-4084-8eb7-27705fd5cf9a", ManualRedirectURL: "https://platform.staging.ant.dev/oauth/code/callback", FileSuffix: "-staging-oauth"}
	}
	if custom := env["CLAUDE_CODE_CUSTOM_OAUTH_URL"]; custom != "" {
		base := strings.TrimSuffix(custom, "/")
		if base != "https://beacon.claude-ai.staging.ant.dev" && base != "https://claude.fedstart.com" && base != "https://claude-staging.fedstart.com" {
			return Config{}, errors.New("CLAUDE_CODE_CUSTOM_OAUTH_URL is not an approved endpoint.")
		}
		c.BaseAPIURL = base
		c.ConsoleAuthorizeURL = base + "/oauth/authorize"
		c.ClaudeAuthorizeURL = c.ConsoleAuthorizeURL
		c.TokenURL = base + "/v1/oauth/token"
		c.ManualRedirectURL = base + "/oauth/code/callback"
		c.FileSuffix = "-custom-oauth"
	}
	if id := env["CLAUDE_CODE_OAUTH_CLIENT_ID"]; id != "" {
		c.ClientID = id
	}
	return c, nil
}
func ParseScopes(value string) []string {
	result := []string{}
	for _, scope := range strings.Split(value, " ") {
		if scope != "" {
			result = append(result, scope)
		}
	}
	return result
}
func BuildAuthURL(c Config, challenge, state string, port float64) (string, error) {
	u, err := url.Parse(c.ClaudeAuthorizeURL)
	if err != nil || u.Scheme == "" {
		return "", errors.New("Invalid URL")
	}
	// URLSearchParams appends in this order; an existing query is retained.
	pairs := [][2]string{{"code", "true"}, {"client_id", c.ClientID}, {"response_type", "code"}, {"redirect_uri", "http://localhost:" + jsNumber(port) + "/callback"}, {"scope", strings.Join(AllScopes, " ")}, {"code_challenge", challenge}, {"code_challenge_method", "S256"}, {"state", state}}
	for _, pair := range pairs {
		if u.RawQuery != "" {
			u.RawQuery += "&"
		}
		u.RawQuery += formEscape(pair[0]) + "=" + formEscape(pair[1])
	}
	return u.String(), nil
}
func formEscape(s string) string {
	value := url.QueryEscape(s)
	value = strings.ReplaceAll(value, "~", "%7E")
	value = strings.ReplaceAll(value, "%2A", "*")
	return value
}
