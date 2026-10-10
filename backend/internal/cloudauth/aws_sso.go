package cloudauth

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

const ssoRefreshMessage = "To refresh this SSO session run 'aws sso login' with the corresponding profile."
const legacySSORefreshMessage = "To refresh this SSO session run aws sso login with the corresponding profile."

// SSOState is the token-provider module's lastRefreshAttemptTime. A CLI process
// shares it across resolvers and children; it is independent of the AWS helper
// result cache and survives an ordinary settings cache reset.
type SSOState struct {
	mu          sync.Mutex
	lastRefresh time.Time
}

type SSO struct {
	Do                         HTTPDo
	State                      *SSOState
	RoleEndpoint, OIDCEndpoint string
}

func SSOTokenPath(home, id string) string {
	hash := sha1.Sum([]byte(id))
	return filepath.Join(home, ".aws", "sso", "cache", hex.EncodeToString(hash[:])+".json")
}

func terminalSSO(message string) error { return &CredentialError{Message: message, TryNext: false} }

func (r *AWSResolver) ssoProfile(ctx context.Context, profile string, data map[string]string) (AWSCredentials, error) {
	if r.options.SSO == nil || r.options.SSO.Do == nil {
		return AWSCredentials{}, fmt.Errorf("%w: actual SSO transport", ErrAWSProviderCapability)
	}
	// fromSSO re-reads config, not the profile object that fromIni received.
	local := map[string]string{}
	for key, value := range data {
		local[key] = value
	}
	sessions, _ := r.ssoSessions(ctx)
	session := local["sso_session"]
	if session != "" {
		values, found := sessions[session]
		if !found {
			return AWSCredentials{}, terminalSSO("undefined is not an object (evaluating 'session.sso_region')")
		}
		local["sso_region"], local["sso_start_url"] = values["sso_region"], values["sso_start_url"]
	}
	for _, key := range []string{"sso_start_url", "sso_account_id", "sso_region", "sso_role_name"} {
		if local[key] == "" {
			keys := r.ssoProfileKeys(ctx, profile)
			for _, added := range []string{"sso_region", "sso_start_url"} {
				if _, present := data[added]; !present && session != "" {
					keys = append(keys, added)
				}
			}
			return AWSCredentials{}, terminalSSO(`Profile is configured with invalid SSO credentials. Required parameters "sso_account_id", "sso_region", "sso_role_name", "sso_start_url". Got ` + strings.Join(keys, ", ") + "\nReference: https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sso.html")
		}
	}
	id := local["sso_start_url"]
	if session != "" {
		id = session
	}
	path := SSOTokenPath(r.options.Home, id)
	raw, err := r.options.ReadFile(ctx, path)
	var token protocol.JSONObject
	if err == nil {
		err = json.Unmarshal(raw, &token)
		if !token.IsObject() && err == nil {
			err = errors.New("SSO token must be an object")
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return AWSCredentials{}, ctx.Err()
		}
		if session != "" {
			return AWSCredentials{}, terminalSSO("The SSO session token associated with profile=" + profile + " was not found or is invalid. " + ssoRefreshMessage)
		}
		return AWSCredentials{}, terminalSSO("The SSO session associated with this profile is invalid. " + legacySSORefreshMessage)
	}
	if session != "" {
		for _, key := range []string{"accessToken", "expiresAt"} {
			if token.Get(key) == nil {
				return AWSCredentials{}, missingSSOToken(key, false)
			}
		}
	}
	expiration, err := ssoDate(token.Get("expiresAt"))
	if err != nil {
		return AWSCredentials{}, err
	}
	if session != "" {
		token, expiration, err = r.refreshSSOToken(ctx, path, token, expiration, local["sso_region"])
		if err != nil {
			return AWSCredentials{}, err
		}
	}
	if expiration-float64(r.options.Now().UnixMilli()) <= 0 {
		return AWSCredentials{}, terminalSSO("The SSO session associated with this profile has expired. " + legacySSORefreshMessage)
	}
	access := token.Get("accessToken")
	if len(access) > 0 && access[0] != '"' {
		return AWSCredentials{}, fmt.Errorf("%w: non-string SSO bearer token", ErrAWSProviderCapability)
	}
	var value string
	_ = json.Unmarshal(access, &value)
	return r.options.SSO.role(ctx, local["sso_region"], local["sso_account_id"], local["sso_role_name"], value)
}

func missingSSOToken(key string, refresh bool) error {
	suffix := ""
	if refresh {
		suffix = ". Cannot refresh"
	}
	return terminalSSO("Value not present for '" + key + "' in SSO Token" + suffix + ". " + ssoRefreshMessage)
}

func ssoDate(raw json.RawMessage) (float64, error) {
	if raw == nil {
		return math.NaN(), nil
	}
	if string(raw) == "null" || string(raw) == "false" {
		return 0, nil
	}
	if string(raw) == "true" {
		return 1, nil
	}
	if len(raw) > 0 && raw[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return 0, err
		}
		ms, valid, err := sessionprojection.ParseDateUTC(units)
		if err != nil {
			return 0, fmt.Errorf("%w: SSO date %v", ErrAWSProviderCapability, err)
		}
		if !valid {
			return math.NaN(), nil
		}
		return ms, nil
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil {
		return math.Trunc(number), nil
	}
	return 0, fmt.Errorf("%w: SSO date coercion", ErrAWSProviderCapability)
}

func (r *AWSResolver) refreshSSOToken(ctx context.Context, path string, token protocol.JSONObject, expiration float64, region string) (protocol.JSONObject, float64, error) {
	now := r.options.Now()
	if expiration-float64(now.UnixMilli()) > 5*60*1000 {
		return token, expiration, nil
	}
	state := r.options.SSO.State
	if state == nil {
		return token, expiration, fmt.Errorf("%w: SSO process refresh state", ErrAWSProviderCapability)
	}
	state.mu.Lock()
	recent := now.Sub(state.lastRefresh) < 30*time.Second
	validateExisting := func() (protocol.JSONObject, float64, error) {
		if expiration < float64(r.options.Now().UnixMilli()) {
			return token, expiration, terminalSSO("Token is expired. " + ssoRefreshMessage)
		}
		return token, expiration, nil
	}
	if recent {
		state.mu.Unlock()
		return validateExisting()
	}
	for _, key := range []string{"clientId", "clientSecret", "refreshToken"} {
		if token.Get(key) == nil {
			state.mu.Unlock()
			return token, expiration, missingSSOToken(key, true)
		}
	}
	state.lastRefresh = now
	state.mu.Unlock()
	refreshed, err := r.options.SSO.refresh(ctx, region, token)
	if err != nil {
		if ctx.Err() != nil {
			return token, expiration, ctx.Err()
		}
		return validateExisting()
	}
	for _, key := range []string{"accessToken", "expiresIn"} {
		if refreshed.Get(key) == nil {
			return validateExisting()
		}
	}
	var seconds float64
	if json.Unmarshal(refreshed.Get("expiresIn"), &seconds) != nil {
		return token, expiration, fmt.Errorf("%w: SSO expiresIn coercion", ErrAWSProviderCapability)
	}
	expiry := float64(r.options.Now().UnixMilli()) + seconds*1000
	stamp := time.UnixMilli(int64(expiry)).UTC().Format("2006-01-02T15:04:05.000Z")
	output := token.Clone()
	output.Set("accessToken", refreshed.Get("accessToken"))
	quoted, _ := json.Marshal(stamp)
	output.Set("expiresAt", quoted)
	output.Set("refreshToken", refreshed.Get("refreshToken"))
	// Source writeFile is best effort, preserves unrelated token properties and
	// does not create parents or replace the token file via a new schema/store.
	if r.options.WriteFile != nil {
		compact, _ := json.Marshal(output)
		compact, err = protocol.ParseAndStringifyJavaScriptJSON(compact)
		if err == nil {
			var pretty bytes.Buffer
			if json.Indent(&pretty, compact, "", "  ") == nil {
				_ = r.options.WriteFile(ctx, path, pretty.Bytes())
			}
		}
	}
	return output, expiry, nil
}

func (r *AWSResolver) configFile() string {
	path := r.options.Environment["AWS_CONFIG_FILE"]
	if path == "" {
		path = filepath.Join(r.options.Home, ".aws", "config")
	}
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(r.options.Home, path[2:])
	}
	return path
}
func (r *AWSResolver) ssoSessions(ctx context.Context) (map[string]map[string]string, map[string][]string) {
	result := map[string]map[string]string{}
	raw, err := r.options.ConfigReadFile(ctx, r.configFile(), false)
	if err != nil {
		return result, nil
	}
	parsed, orders, err := parseINIWithOrder(string(raw))
	if err != nil {
		return result, nil
	}
	for key, value := range parsed {
		if strings.HasPrefix(key, "sso-session.") {
			result[strings.TrimPrefix(key, "sso-session.")] = value
		}
	}
	return result, orders
}
func (r *AWSResolver) ssoProfileKeys(ctx context.Context, profile string) []string {
	keys := []string{}
	seen := map[string]bool{}
	for i, key := range []string{"AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE"} {
		path := r.options.Environment[key]
		if path == "" {
			name := "config"
			if i == 1 {
				name = "credentials"
			}
			path = filepath.Join(r.options.Home, ".aws", name)
		}
		if strings.HasPrefix(path, "~/") {
			path = filepath.Join(r.options.Home, path[2:])
		}
		raw, err := r.options.ConfigReadFile(ctx, path, false)
		if err != nil {
			continue
		}
		_, orders, err := parseINIWithOrder(string(raw))
		if err != nil {
			continue
		}
		name := profile
		if i == 0 && profile != "default" {
			name = "profile." + profile
		}
		for _, key := range orders[name] {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}

func (s SSO) endpoint(region string, oidc bool) string {
	if oidc && s.OIDCEndpoint != "" {
		return s.OIDCEndpoint
	}
	if !oidc && s.RoleEndpoint != "" {
		return s.RoleEndpoint
	}
	host := "portal.sso"
	if oidc {
		host = "oidc"
	}
	suffix := "amazonaws.com"
	if strings.HasPrefix(region, "cn-") {
		suffix += ".cn"
	}
	return "https://" + host + "." + region + "." + suffix
}
func (s SSO) invoke(ctx context.Context, method, endpoint, path string, body []byte, headers map[string]string) (protocol.JSONObject, error) {
	base, err := ParseEndpoint(endpoint)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := s.Do(request)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if response == nil || response.Body == nil {
		return protocol.JSONObject{}, errors.New("SSO transport returned no response body")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if len(raw) > 2*1024*1024 {
		return protocol.JSONObject{}, errors.New("SSO response exceeds bounded credential envelope")
	}
	var output protocol.JSONObject
	if json.Unmarshal(raw, &output) != nil {
		return output, errors.New("SSO response is not JSON")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var message string
		_ = json.Unmarshal(output.Get("message"), &message)
		if message == "" {
			message = "UnknownError"
		}
		name := response.Header.Get("x-amzn-errortype")
		if name == "" {
			_ = json.Unmarshal(output.Get("code"), &name)
		}
		if name == "" {
			_ = json.Unmarshal(output.Get("__type"), &name)
		}
		if name == "" {
			name = "UnknownError"
		}
		name = strings.Split(name, ":")[0]
		if index := strings.LastIndex(name, "#"); index >= 0 {
			name = name[index+1:]
		}
		return output, &ssoHTTPError{name: name, message: message}
	}
	return output, nil
}
func (s SSO) refresh(ctx context.Context, region string, token protocol.JSONObject) (protocol.JSONObject, error) {
	body := protocol.NewJSONObject(protocol.JSONField{Name: "clientId", Value: token.Get("clientId")}, protocol.JSONField{Name: "clientSecret", Value: token.Get("clientSecret")}, protocol.JSONField{Name: "grantType", Value: json.RawMessage(`"refresh_token"`)}, protocol.JSONField{Name: "refreshToken", Value: token.Get("refreshToken")})
	raw, err := json.Marshal(body)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return s.invoke(ctx, http.MethodPost, s.endpoint(region, true), "/token", raw, map[string]string{"Content-Type": "application/json"})
}
func (s SSO) role(ctx context.Context, region, account, role, token string) (AWSCredentials, error) {
	endpoint, err := ParseEndpoint(s.endpoint(region, false))
	if err != nil {
		return AWSCredentials{}, err
	}
	query := endpoint.Query()
	query.Set("role_name", role)
	query.Set("account_id", account)
	endpoint.RawQuery = query.Encode()
	headers := map[string]string{}
	if token != "" {
		headers["x-amz-sso_bearer_token"] = token
	}
	output, err := s.invoke(ctx, http.MethodGet, endpoint.String(), "/federation/credentials", nil, headers)
	if err != nil {
		if ctx.Err() != nil {
			return AWSCredentials{}, ctx.Err()
		}
		var api *ssoHTTPError
		if errors.As(err, &api) {
			return AWSCredentials{}, terminalSSO(err.Error())
		}
		return AWSCredentials{}, terminalSSO("Error: " + err.Error())
	}
	credentials, _ := output.Object("roleCredentials")
	value := AWSCredentials{}
	_ = json.Unmarshal(credentials.Get("accessKeyId"), &value.AccessKeyID)
	_ = json.Unmarshal(credentials.Get("secretAccessKey"), &value.SecretAccessKey)
	var session string
	_ = json.Unmarshal(credentials.Get("sessionToken"), &session)
	var expiration float64
	_ = json.Unmarshal(credentials.Get("expiration"), &expiration)
	if value.AccessKeyID == "" || value.SecretAccessKey == "" || session == "" || expiration == 0 {
		return AWSCredentials{}, terminalSSO("SSO returns an invalid temporary credential.")
	}
	stamp := time.UnixMilli(int64(expiration))
	value.Expiration = &stamp
	value.SessionToken = &session
	_ = json.Unmarshal(credentials.Get("credentialScope"), &value.CredentialScope)
	_ = json.Unmarshal(credentials.Get("accountId"), &value.AccountID)
	return value, nil
}

type ssoHTTPError struct{ name, message string }

func (e *ssoHTTPError) Error() string { return e.name + ": " + e.message }
