package cloudauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// Metadata binds only the original credential HTTP primitive. Selection still
// comes from the frozen AWS chain; no metadata endpoint is probed by a direct
// Anthropic/DeepSeek session.
type Metadata struct {
	Do     HTTPDo
	Sleep  func(context.Context, time.Duration) error
	Random func() float64
}

const metadataURLMessage = "URL not accepted. It must either be HTTPS or match one of the following:\n  - loopback CIDR 127.0.0.0/8 or [::1/128]\n  - ECS container host 169.254.170.2\n  - EKS container host 169.254.170.23 or [fd00:ec2::23]"

func CheckContainerURL(raw string) (string, error) {
	parsed, err := weburl.Parse(raw)
	if err != nil {
		return "", err
	}
	hostname := parsed.Hostname()
	valid := parsed.Protocol() == "https:" || hostname == "169.254.170.2" || hostname == "169.254.170.23" || hostname == "[fd00:ec2::23]" || hostname == "[::1]" || hostname == "localhost"
	if !valid && !strings.Contains(hostname, "[") {
		ip := net.ParseIP(hostname)
		valid = ip != nil && ip.To4() != nil && ip.To4()[0] == 127
	}
	if !valid {
		return "", &CredentialError{Message: metadataURLMessage, TryNext: true}
	}
	// createGetRequest ignores URL userinfo and reduces duplicate query keys to
	// their last value. NodeHttpHandler does not synthesize Basic authorization.
	target, err := url.Parse(parsed.String())
	if err != nil {
		return "", err
	}
	target.User = nil
	target.Fragment = ""
	query := target.Query()
	for key, values := range query {
		if len(values) > 0 {
			query.Set(key, values[len(values)-1])
		}
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func (r *AWSResolver) container(ctx context.Context, legacy bool) (AWSCredentials, error) {
	if r.options.Metadata == nil || r.options.Metadata.Do == nil {
		return AWSCredentials{}, fmt.Errorf("%w: actual container metadata transport", ErrAWSProviderCapability)
	}
	env := r.options.Environment
	full, relative := env["AWS_CONTAINER_CREDENTIALS_FULL_URI"], env["AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"]
	if legacy {
		return r.containerLegacy(ctx, full, relative)
	}
	endpoint := full
	if endpoint == "" {
		endpoint = "http://169.254.170.2" + relative
	}
	endpoint, err := CheckContainerURL(endpoint)
	if err != nil {
		return AWSCredentials{}, err
	}
	parsed, _ := url.Parse(endpoint)
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return AWSCredentials{}, fmt.Errorf("%w: HTTP metadata non-HTTP protocol", ErrAWSProviderCapability)
	}
	for attempt := 0; attempt < 4; attempt++ {
		var token string
		token = env["AWS_CONTAINER_AUTHORIZATION_TOKEN"]
		if token == "" && env["AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"] != "" {
			var raw []byte
			raw, err = r.options.ReadFile(ctx, env["AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"])
			if err == nil {
				token = string(protocol.DecodeJavaScriptUTF8(raw))
			}
		} else {
			err = nil
		}
		if err == nil {
			var raw []byte
			var status int
			raw, status, err = r.options.Metadata.get(ctx, endpoint, token, time.Second)
			if err == nil && status != 200 {
				err = &CredentialError{Message: fmt.Sprintf("Server responded with status: %d", status), TryNext: true}
			}
			if err == nil {
				var value AWSCredentials
				value, err = decodeMetadataCredentials(raw, true)
				if err == nil {
					return value, nil
				}
			}
		}
		if ctx.Err() != nil {
			return AWSCredentials{}, ctx.Err()
		}
		if attempt < 3 {
			if err = r.options.Metadata.sleep(ctx, time.Second); err != nil {
				return AWSCredentials{}, err
			}
		}
	}
	// remoteProvider chains the older container provider after all four modern
	// HTTP attempts. Its relative URI and authorization-token precedence differ.
	return r.containerLegacy(ctx, full, relative)
}

func (r *AWSResolver) containerLegacy(ctx context.Context, full, relative string) (AWSCredentials, error) {
	endpoint := ""
	if relative != "" {
		endpoint = "http://169.254.170.2" + relative
	} else if full != "" {
		parsed, err := url.Parse(full)
		if err != nil {
			return AWSCredentials{}, err
		}
		host := parsed.Hostname()
		if host != "localhost" && host != "127.0.0.1" {
			return AWSCredentials{}, &CredentialError{Message: host + " is not a valid container metadata service hostname", TryNext: false}
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return AWSCredentials{}, &CredentialError{Message: parsed.Scheme + ": is not a valid container metadata service protocol", TryNext: false}
		}
		if parsed.Scheme == "https" {
			return AWSCredentials{}, fmt.Errorf("%w: legacy Node http.request HTTPS rejection", ErrAWSProviderCapability)
		}
		endpoint = full
	} else {
		return AWSCredentials{}, &CredentialError{Message: "The container metadata credential provider cannot be used unless the AWS_CONTAINER_CREDENTIALS_RELATIVE_URI or AWS_CONTAINER_CREDENTIALS_FULL_URI environment variable is set", TryNext: false}
	}
	raw, status, err := r.options.Metadata.get(ctx, endpoint, r.options.Environment["AWS_CONTAINER_AUTHORIZATION_TOKEN"], time.Second)
	if err != nil {
		return AWSCredentials{}, err
	}
	if status < 200 || status >= 300 {
		return AWSCredentials{}, &CredentialError{Message: "Error response received from instance metadata service", TryNext: true}
	}
	return decodeMetadataCredentials(raw, false)
}

func (m Metadata) sleep(ctx context.Context, d time.Duration) error {
	if m.Sleep != nil {
		return m.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m Metadata) get(ctx context.Context, endpoint, token string, timeout time.Duration) ([]byte, int, error) {
	active, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(active, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	if token != "" {
		request.Header.Set("Authorization", token)
	}
	response, err := m.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, err
	}
	if response == nil || response.Body == nil {
		return nil, 0, errors.New("metadata transport returned no response body")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(raw) > 2*1024*1024 {
		return nil, response.StatusCode, fmt.Errorf("%w: bounded metadata response", ErrAWSProviderCapability)
	}
	return raw, response.StatusCode, nil
}

func decodeMetadataCredentials(raw []byte, modern bool) (AWSCredentials, error) {
	var object protocol.JSONObject
	if json.Unmarshal(raw, &object) != nil {
		return AWSCredentials{}, &CredentialError{Message: "metadata response is not JSON", TryNext: true}
	}
	values := map[string]string{}
	for _, key := range []string{"AccessKeyId", "SecretAccessKey", "Token", "Expiration"} {
		data := object.Get(key)
		if len(data) == 0 || data[0] != '"' {
			message := "Invalid response received from instance metadata service."
			if modern {
				message = "HTTP credential provider response not of the required format, an object matching: { AccessKeyId: string, SecretAccessKey: string, Token: string, Expiration: string(rfc3339) }"
			}
			return AWSCredentials{}, &CredentialError{Message: message, TryNext: true}
		}
		var value string
		_ = json.Unmarshal(data, &value)
		values[key] = value
	}
	var expiration time.Time
	var err error
	if modern {
		expiration, err = parseAWSRFC3339(values["Expiration"])
		if err != nil {
			return AWSCredentials{}, &CredentialError{Message: err.Error(), TryNext: true}
		}
	} else {
		ms, err := ssoDate(object.Get("Expiration"))
		if err != nil {
			return AWSCredentials{}, err
		}
		expiration = time.UnixMilli(int64(ms))
	}
	token := values["Token"]
	value := AWSCredentials{AccessKeyID: values["AccessKeyId"], SecretAccessKey: values["SecretAccessKey"], SessionToken: &token, Expiration: &expiration}
	if !modern {
		_ = json.Unmarshal(object.Get("AccountId"), &value.AccountID)
	}
	return value, nil
}
