package desktopoauth

import (
	"errors"
	"net/url"
	"strings"
)

// CLIAuthURL retains the original shared OAuth configuration and URL encoding;
// only the actual CLI manual/Console/hint options differ from the desktop flow.
type CLIAuthURL struct {
	Challenge, State                 string
	Port                             float64
	Manual, ClaudeAI, InferenceOnly  bool
	Organization, Email, LoginMethod string
}

func BuildCLIAuthURL(c Config, o CLIAuthURL) (string, error) {
	address := c.ConsoleAuthorizeURL
	if o.ClaudeAI {
		address = c.ClaudeAuthorizeURL
	}
	u, e := url.Parse(address)
	if e != nil || u.Scheme == "" {
		return "", errors.New("Invalid URL")
	}
	redirect := "http://localhost:" + jsNumber(o.Port) + "/callback"
	if o.Manual {
		redirect = c.ManualRedirectURL
	}
	scopes := AllScopes
	if o.InferenceOnly {
		scopes = []string{"user:inference"}
	}
	pairs := [][2]string{{"code", "true"}, {"client_id", c.ClientID}, {"response_type", "code"}, {"redirect_uri", redirect}, {"scope", strings.Join(scopes, " ")}, {"code_challenge", o.Challenge}, {"code_challenge_method", "S256"}, {"state", o.State}}
	for _, pair := range [][2]string{{"orgUUID", o.Organization}, {"login_hint", o.Email}, {"login_method", o.LoginMethod}} {
		if pair[1] != "" {
			pairs = append(pairs, pair)
		}
	}
	for _, pair := range pairs {
		if u.RawQuery != "" {
			u.RawQuery += "&"
		}
		u.RawQuery += formEscape(pair[0]) + "=" + formEscape(pair[1])
	}
	return u.String(), nil
}
