package cloudauth

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type CredentialError struct {
	Message string
	TryNext bool
}

func (e *CredentialError) Error() string { return e.Message }

func staticProfile(data map[string]string) (AWSCredentials, bool) {
	access, a := data["aws_access_key_id"]
	secret, b := data["aws_secret_access_key"]
	if !a || !b {
		return AWSCredentials{}, false
	}
	value := AWSCredentials{AccessKeyID: access, SecretAccessKey: secret, CredentialScope: data["aws_credential_scope"], AccountID: data["aws_account_id"]}
	if token, exists := data["aws_session_token"]; exists {
		value.SessionToken = &token
	}
	return value, true
}

func (r *AWSResolver) resolveProfile(ctx context.Context, name, top string, profiles map[string]map[string]string, visited []string, recursive bool) (AWSCredentials, error) {
	data := profiles[name]
	if len(visited) > 0 {
		if value, ok := staticProfile(data); ok {
			return value, nil
		}
	}
	_, role := data["role_arn"]
	source, hasSource := data["source_profile"]
	credentialSource, hasCredentialSource := data["credential_source"]
	if recursive || role && (hasSource != hasCredentialSource) {
		if source != "" {
			for _, value := range visited {
				if value == source {
					return AWSCredentials{}, &CredentialError{Message: "Detected a cycle attempting to resolve credentials for profile " + top + ". Profiles visited: " + strings.Join(visited, ", "), TryNext: true}
				}
			}
		}
		if serial := data["mfa_serial"]; serial != "" {
			return AWSCredentials{}, &CredentialError{Message: "Profile " + name + " requires multi-factor authentication, but no MFA code callback was provided.", TryNext: false}
		}
		var base AWSCredentials
		var err error
		if source != "" {
			next := append(append([]string{}, visited...), source)
			child := profiles[source]
			base, err = r.resolveProfile(ctx, source, top, profiles, next, child["role_arn"] == "" && child["credential_source"] != "")
		} else {
			switch credentialSource {
			case "Environment":
				base, err = FromEnvironment(r.options.Environment)
			case "EcsContainer":
				base, err = r.container(ctx, true)
			case "Ec2InstanceMetadata":
				base, err = NewInstanceProvider(r).Resolve(ctx)
			default:
				err = &CredentialError{Message: "Unsupported credential source in profile " + name + ". Got " + credentialSource + ", expected EcsContainer or Ec2InstanceMetadata or Environment.", TryNext: true}
			}
		}
		if err != nil {
			return AWSCredentials{}, err
		}
		if data["role_arn"] == "" && credentialSource != "" {
			return base, nil
		}
		if r.options.STS == nil {
			return AWSCredentials{}, fmt.Errorf("%w: actual STS role transport", ErrAWSProviderCapability)
		}
		sts := *r.options.STS
		if data["region"] != "" {
			sts.Region = data["region"]
		}
		session := data["role_session_name"]
		if session == "" {
			session = "aws-sdk-js-" + strconv.FormatInt(r.options.Now().UnixMilli(), 10)
		}
		duration := data["duration_seconds"]
		if duration == "" {
			duration = "3600"
		}
		number, ok := jsParseInt(duration)
		if !ok {
			return AWSCredentials{}, fmt.Errorf("%w: NaN assume-role duration", ErrAWSProviderCapability)
		}
		params := [][2]string{{"RoleArn", data["role_arn"]}, {"RoleSessionName", session}}
		if external, exists := data["external_id"]; exists {
			params = append(params, [2]string{"ExternalId", external})
		}
		params = append(params, [2]string{"DurationSeconds", strconv.FormatInt(number, 10)})
		return sts.AssumeRole(ctx, base, params)
	}
	if value, ok := staticProfile(data); ok {
		return value, nil
	}
	if path, ok := data["web_identity_token_file"]; ok && role {
		session, present := data["role_session_name"]
		return r.webIdentity(ctx, path, data["role_arn"], session, present)
	}
	if command, ok := data["credential_process"]; ok {
		return r.process(ctx, name, command, data["aws_account_id"])
	}
	for _, key := range []string{"sso_session", "sso_start_url", "sso_account_id", "sso_region", "sso_role_name"} {
		if _, exists := data[key]; exists {
			return r.ssoProfile(ctx, name, data)
		}
	}
	if _, exists := data["login_session"]; exists {
		return AWSCredentials{}, fmt.Errorf("%w: INI login_session", ErrAWSProviderCapability)
	}
	return AWSCredentials{}, &CredentialError{Message: "Could not resolve credentials using profile: [" + name + "] in configuration/credentials file(s).", TryNext: true}
}

func jsParseInt(value string) (int64, bool) {
	value = strings.TrimLeftFunc(value, jsSpace)
	i := 0
	if len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		i++
	}
	start := i
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		i++
	}
	if i == start {
		return 0, false
	}
	number, err := strconv.ParseInt(value[:i], 10, 64)
	return number, err == nil
}

func (r *AWSResolver) webIdentity(ctx context.Context, path, role, session string, sessionPresent bool) (AWSCredentials, error) {
	if r.options.STS == nil {
		return AWSCredentials{}, fmt.Errorf("%w: actual STS web identity transport", ErrAWSProviderCapability)
	}
	raw, err := r.options.ReadFile(ctx, path)
	if err != nil {
		return AWSCredentials{}, err
	}
	// Original readFileSync encoding:'ascii' clears each high bit, rather than
	// trimming whitespace or parsing the token as UTF-8.
	for index := range raw {
		raw[index] &= 0x7f
	}
	if !sessionPresent {
		session = "aws-sdk-js-session-" + strconv.FormatInt(r.options.Now().UnixMilli(), 10)
	}
	return r.options.STS.AssumeRoleWithWebIdentity(ctx, [][2]string{{"RoleArn", role}, {"RoleSessionName", session}, {"WebIdentityToken", string(raw)}})
}

func (r *AWSResolver) process(ctx context.Context, profile, command, profileAccount string) (AWSCredentials, error) {
	if r.options.Run == nil {
		return AWSCredentials{}, fmt.Errorf("%w: actual credential_process", ErrAWSProviderCapability)
	}
	result, err := r.options.Run(ctx, command, 0)
	if err != nil {
		return AWSCredentials{}, &CredentialError{Message: err.Error(), TryNext: true}
	}
	if result.ExitCode != 0 {
		return AWSCredentials{}, &CredentialError{Message: "credential_process exited unsuccessfully", TryNext: true}
	}
	var data map[string]json.RawMessage
	if json.Unmarshal([]byte(strings.TrimFunc(result.Stdout, jsSpace)), &data) != nil {
		return AWSCredentials{}, &CredentialError{Message: "Profile " + profile + " credential_process returned invalid JSON.", TryNext: true}
	}
	var version float64
	if json.Unmarshal(data["Version"], &version) != nil || version != 1 {
		return AWSCredentials{}, &CredentialError{Message: "Profile " + profile + " credential_process did not return Version 1.", TryNext: true}
	}
	access, a := data["AccessKeyId"]
	secret, b := data["SecretAccessKey"]
	if !a || !b {
		return AWSCredentials{}, &CredentialError{Message: "Profile " + profile + " credential_process returned invalid credentials.", TryNext: true}
	}
	value := AWSCredentials{}
	if json.Unmarshal(access, &value.AccessKeyID) != nil || json.Unmarshal(secret, &value.SecretAccessKey) != nil {
		return AWSCredentials{}, fmt.Errorf("%w: non-string process credential", ErrAWSProviderCapability)
	}
	decode := func(key string) string { var value string; _ = json.Unmarshal(data[key], &value); return value }
	if token := decode("SessionToken"); token != "" {
		value.SessionToken = &token
	}
	if expiry := decode("Expiration"); expiry != "" {
		stamp, err := time.Parse(time.RFC3339Nano, expiry)
		if err != nil {
			return AWSCredentials{}, fmt.Errorf("%w: invalid JavaScript process expiration", ErrAWSProviderCapability)
		}
		if stamp.Before(r.options.Now()) {
			return AWSCredentials{}, &CredentialError{Message: "Profile " + profile + " credential_process returned expired credentials.", TryNext: true}
		}
		value.Expiration = &stamp
	}
	value.CredentialScope = decode("CredentialScope")
	value.AccountID = decode("AccountId")
	if value.AccountID == "" {
		value.AccountID = profileAccount
	}
	return value, nil
}
