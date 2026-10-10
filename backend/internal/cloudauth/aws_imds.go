package cloudauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// InstanceProvider retains exactly one fromInstanceMetadata invocation's token
// fallback and static-stability state. The normal wrapper creates a fresh node
// provider chain for each getAuthHeaders, so it must not become a CLI-global
// credential cache or share past credentials with an unrelated provider.
type InstanceProvider struct {
	resolver          *AWSResolver
	disableFetchToken bool
	past              *AWSCredentials
}

func NewInstanceProvider(resolver *AWSResolver) *InstanceProvider {
	return &InstanceProvider{resolver: resolver}
}

type MetadataError struct {
	Message string
	Status  int
}

func (e *MetadataError) Error() string { return e.Message }

func (p *InstanceProvider) Resolve(ctx context.Context) (AWSCredentials, error) {
	value, err := p.resolve(ctx)
	if ctx.Err() != nil {
		return AWSCredentials{}, ctx.Err()
	}
	if err != nil {
		if p.past == nil {
			return AWSCredentials{}, err
		}
		value = *p.past
		value = p.extend(value)
	} else if value.Expiration != nil && value.Expiration.Before(p.resolver.options.Now()) {
		value = p.extend(value)
	}
	copied := value
	p.past = &copied
	return value, nil
}
func (p *InstanceProvider) extend(value AWSCredentials) AWSCredentials {
	random := rand.Float64()
	if p.resolver.options.Metadata.Random != nil {
		random = p.resolver.options.Metadata.Random()
	}
	if value.OriginalExpiration == nil {
		value.OriginalExpiration = value.Expiration
	}
	next := p.resolver.options.Now().Add(time.Duration(300+math.Floor(random*300)) * time.Second)
	value.Expiration = &next
	return value
}
func (p *InstanceProvider) resolve(ctx context.Context) (AWSCredentials, error) {
	r := p.resolver
	if r == nil || r.options.Metadata == nil || r.options.Metadata.Do == nil {
		return AWSCredentials{}, fmt.Errorf("%w: actual instance metadata transport", ErrAWSProviderCapability)
	}
	endpoint, err := r.instanceEndpoint(ctx)
	if err != nil {
		return AWSCredentials{}, err
	}
	var token *string
	if !p.disableFetchToken {
		raw, err := r.options.Metadata.request(ctx, http.MethodPut, endpoint, "/latest/api/token", map[string]string{"x-aws-ec2-metadata-token-ttl-seconds": "21600"})
		if err != nil {
			if ctx.Err() != nil {
				return AWSCredentials{}, ctx.Err()
			}
			var failure *MetadataError
			if errors.As(err, &failure) && failure.Status == 400 {
				return AWSCredentials{}, &MetadataError{Message: "EC2 Metadata token request returned error", Status: 400}
			}
			if err.Error() == "TimeoutError" || errors.As(err, &failure) && (failure.Status == 403 || failure.Status == 404 || failure.Status == 405) {
				p.disableFetchToken = true
			}
		} else {
			value := string(raw)
			token = &value
		}
	}
	if token == nil {
		if err = r.validateV1(ctx); err != nil {
			return AWSCredentials{}, err
		}
	}
	headers := map[string]string{}
	if token != nil {
		headers["x-aws-ec2-metadata-token"] = *token
	}
	raw, err := r.options.Metadata.request(ctx, http.MethodGet, endpoint, "/latest/meta-data/iam/security-credentials/", headers)
	if err != nil {
		p.resetOn401(err)
		return AWSCredentials{}, err
	}
	profile := strings.TrimFunc(string(raw), jsSpace)
	raw, err = r.options.Metadata.request(ctx, http.MethodGet, endpoint, "/latest/meta-data/iam/security-credentials/"+profile, headers)
	if err != nil {
		p.resetOn401(err)
		return AWSCredentials{}, err
	}
	return decodeMetadataCredentials(raw, false)
}
func (p *InstanceProvider) resetOn401(err error) {
	var failure *MetadataError
	if errors.As(err, &failure) && failure.Status == 401 {
		p.disableFetchToken = false
	}
}
func (r *AWSResolver) validateV1(ctx context.Context) error {
	value, present := r.options.Environment["AWS_EC2_METADATA_V1_DISABLED"]
	cause := "process environment variable (AWS_EC2_METADATA_V1_DISABLED)"
	if !present {
		value = r.configPreferredProfile(ctx)["ec2_metadata_v1_disabled"]
		cause = "config file profile (ec2_metadata_v1_disabled)"
	}
	if value != "" && value != "false" {
		return &CredentialError{Message: "AWS EC2 Metadata v1 fallback has been blocked by AWS SDK configuration in the following: [" + cause + "].", TryNext: true}
	}
	return nil
}
func (r *AWSResolver) instanceEndpoint(ctx context.Context) (string, error) {
	value, present := r.options.Environment["AWS_EC2_METADATA_SERVICE_ENDPOINT"]
	profile := r.configPreferredProfile(ctx)
	if !present {
		value = profile["ec2_metadata_service_endpoint"]
	}
	if value != "" {
		parsed, err := ParseEndpoint(value)
		if err != nil {
			return "", err
		}
		return parsed.String(), nil
	}
	mode, present := r.options.Environment["AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE"]
	if !present {
		mode = profile["ec2_metadata_service_endpoint_mode"]
		if mode == "" {
			mode = "IPv4"
		}
	}
	switch mode {
	case "IPv4":
		return "http://169.254.169.254", nil
	case "IPv6":
		return "http://[fd00:ec2::254]", nil
	default:
		return "", fmt.Errorf("Unsupported endpoint mode: %s. Select from IPv4,IPv6", mode)
	}
}
func (r *AWSResolver) configPreferredProfile(ctx context.Context) map[string]string {
	merged := map[string]string{}
	profile := r.options.Environment["AWS_PROFILE"]
	if profile == "" {
		profile = "default"
	}
	for index, key := range []string{"AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE"} {
		path := r.options.Environment[key]
		if path == "" {
			name := "credentials"
			if index == 1 {
				name = "config"
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
		parsed, err := ParseINI(string(raw))
		if err != nil {
			continue
		}
		name := profile
		if index == 1 && profile != "default" {
			name = "profile." + profile
		}
		for key, value := range parsed[name] {
			merged[key] = value
		}
	}
	return merged
}
func (m Metadata) request(ctx context.Context, method, endpoint, path string, headers map[string]string) ([]byte, error) {
	active, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	request, err := http.NewRequestWithContext(active, method, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := m.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if active.Err() != nil {
			return nil, &MetadataError{Message: "TimeoutError from instance metadata service"}
		}
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, errors.New("instance metadata transport returned no response body")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &MetadataError{Message: "Error response received from instance metadata service", Status: response.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 2*1024*1024 {
		return nil, fmt.Errorf("%w: bounded metadata response", ErrAWSProviderCapability)
	}
	return raw, nil
}
