package cloudauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrAWSProviderCapability = errors.New("AWS credential provider capability is not migrated")

type AWSOptions struct {
	Environment    map[string]string
	Home           string
	ReadFile       func(context.Context, string) ([]byte, error)
	ConfigReadFile func(context.Context, string, bool) ([]byte, error)
	Now            func() time.Time
	STS            *STS
	Run            CloudCommand
	SSO            *SSO
	Metadata       *Metadata
	WriteFile      func(context.Context, string, []byte) error
}

type AWSResolver struct{ options AWSOptions }

func NewAWSResolver(options AWSOptions) (*AWSResolver, error) {
	if options.ReadFile == nil || options.Home == "" || !filepath.IsAbs(options.Home) {
		return nil, errors.New("AWS credentials require explicit home and file reader")
	}
	env := map[string]string{}
	for key, value := range options.Environment {
		env[key] = value
	}
	options.Environment = env
	if options.ConfigReadFile == nil {
		cache := &AWSFileCache{}
		options.ConfigReadFile = func(ctx context.Context, path string, ignore bool) ([]byte, error) {
			return cache.Read(ctx, path, ignore, options.ReadFile)
		}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &AWSResolver{options: options}, nil
}

// FromEnvironment is @aws-sdk/credential-provider-env's source, before the
// default chain's AWS_PROFILE gate. An empty token is absent, not a null token.
func FromEnvironment(env map[string]string) (AWSCredentials, error) {
	access, secret := env["AWS_ACCESS_KEY_ID"], env["AWS_SECRET_ACCESS_KEY"]
	if access == "" || secret == "" {
		return AWSCredentials{}, errors.New("Unable to find environment variable credentials.")
	}
	value := AWSCredentials{AccessKeyID: access, SecretAccessKey: secret, CredentialScope: env["AWS_CREDENTIAL_SCOPE"], AccountID: env["AWS_ACCOUNT_ID"]}
	if token := env["AWS_SESSION_TOKEN"]; token != "" {
		value.SessionToken = &token
	}
	if expiry := env["AWS_CREDENTIAL_EXPIRATION"]; expiry != "" {
		stamp, err := time.Parse(time.RFC3339Nano, expiry)
		if err != nil {
			return AWSCredentials{}, fmt.Errorf("%w: invalid JavaScript credential expiration", ErrAWSProviderCapability)
		}
		value.Expiration = &stamp
	}
	return value, nil
}

func (r *AWSResolver) Resolve(ctx context.Context) (AWSCredentials, error) {
	if err := ctx.Err(); err != nil {
		return AWSCredentials{}, err
	}
	env := r.options.Environment
	if env["AWS_PROFILE"] == "" {
		if value, err := FromEnvironment(env); err == nil {
			return value, nil
		} else if errors.Is(err, ErrAWSProviderCapability) {
			return AWSCredentials{}, err
		}
	}
	profiles := r.profiles(ctx)
	profile := env["AWS_PROFILE"]
	if profile == "" {
		profile = "default"
	}
	if _, ok := profiles[profile]; ok {
		value, err := r.resolveProfile(ctx, profile, profile, profiles, nil, false)
		if err == nil || errors.Is(err, ErrAWSProviderCapability) {
			return value, err
		}
		var terminal *CredentialError
		if errors.As(err, &terminal) && !terminal.TryNext {
			return AWSCredentials{}, err
		}
	}
	if command, present := profiles[profile]["credential_process"]; present {
		value, err := r.process(ctx, profile, command, profiles[profile]["aws_account_id"])
		if err == nil || errors.Is(err, ErrAWSProviderCapability) {
			return value, err
		}
		var terminal *CredentialError
		if errors.As(err, &terminal) && !terminal.TryNext {
			return AWSCredentials{}, err
		}
	}
	if env["AWS_WEB_IDENTITY_TOKEN_FILE"] != "" && env["AWS_ROLE_ARN"] != "" {
		return r.webIdentity(ctx, env["AWS_WEB_IDENTITY_TOKEN_FILE"], env["AWS_ROLE_ARN"], env["AWS_ROLE_SESSION_NAME"], hasEnvironment(env, "AWS_ROLE_SESSION_NAME"))
	}
	if env["AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"] != "" || env["AWS_CONTAINER_CREDENTIALS_FULL_URI"] != "" {
		value, err := r.container(ctx, false)
		var terminal *CredentialError
		if errors.As(err, &terminal) && terminal.TryNext {
			return AWSCredentials{}, &CredentialError{Message: "Could not load credentials from any providers", TryNext: false}
		}
		return value, err
	}
	if disabled := env["AWS_EC2_METADATA_DISABLED"]; disabled != "" && disabled != "false" {
		return AWSCredentials{}, errors.New("Could not load credentials from any providers")
	}
	value, err := NewInstanceProvider(r).Resolve(ctx)
	var terminal *CredentialError
	var metadata *MetadataError
	if errors.As(err, &metadata) || errors.As(err, &terminal) && terminal.TryNext {
		return AWSCredentials{}, &CredentialError{Message: "Could not load credentials from any providers", TryNext: false}
	}
	return value, err
}

func (r *AWSResolver) profiles(ctx context.Context, force ...bool) map[string]map[string]string {
	merged := map[string]map[string]string{}
	for index, key := range []string{"AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE"} {
		path := r.options.Environment[key]
		if path == "" {
			name := "config"
			if index == 1 {
				name = "credentials"
			}
			path = filepath.Join(r.options.Home, ".aws", name)
		}
		if strings.HasPrefix(path, "~/") {
			path = filepath.Join(r.options.Home, path[2:])
		}
		raw, err := r.options.ConfigReadFile(ctx, path, len(force) > 0 && force[0])
		if err != nil {
			continue
		}
		parsed, err := ParseINI(string(raw))
		if err != nil {
			continue
		}
		for name, values := range parsed {
			if index == 0 {
				if name != "default" {
					prefix, rest, found := strings.Cut(name, ".")
					if !found || (prefix != "profile" && prefix != "sso-session" && prefix != "services") {
						continue
					}
					if prefix == "profile" {
						name = rest
					}
				}
			}
			if merged[name] == nil {
				merged[name] = map[string]string{}
			}
			for key, value := range values {
				merged[name][key] = value
			}
		}
	}
	return merged
}

// ResolveINI performs the actual fromIni consumer after a refresh/export. It
// never substitutes an environment credential for the INI cache refresh.
func (r *AWSResolver) ResolveINI(ctx context.Context) (AWSCredentials, error) {
	profile := r.options.Environment["AWS_PROFILE"]
	if profile == "" {
		profile = "default"
	}
	return r.resolveProfile(ctx, profile, profile, r.profiles(ctx, true), nil, false)
}

var iniPrefix = regexp.MustCompile(`^([\w-]+)[\s\x{00A0}\x{FEFF}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}](["']?)([\w\-@+.%:/]+)(["']?)$`)

// ParseINI ports Smithy's section-prefix, comment and nested-subsection rules.
// Bad files are swallowed by loadSharedConfigFiles, not by this parser.
func ParseINI(data string) (map[string]map[string]string, error) {
	result, _, err := parseINIWithOrder(data)
	return result, err
}

func parseINIWithOrder(data string) (map[string]map[string]string, map[string][]string, error) {
	result := map[string]map[string]string{}
	orders := map[string][]string{}
	section, subsection := "", ""
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := line
		for i, ch := range line {
			if (ch == ';' || ch == '#') && (i == 0 || previousSpace(line[:i])) {
				trimmed = line[:i]
				break
			}
		}
		trimmed = strings.TrimFunc(trimmed, jsSpace)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section, subsection = "", ""
			name := trimmed[1 : len(trimmed)-1]
			if name == "__proto__" || name == "profile __proto__" {
				return nil, nil, fmt.Errorf("Found invalid profile name %q", name)
			}
			if parts := iniPrefix.FindStringSubmatch(name); parts != nil && parts[2] == parts[4] {
				if parts[1] == "profile" || parts[1] == "sso-session" || parts[1] == "services" {
					section = parts[1] + "." + parts[3]
				}
			} else {
				section = name
			}
			continue
		}
		if section == "" {
			continue
		}
		name, value, found := strings.Cut(trimmed, "=")
		if !found || name == "" {
			continue
		}
		name = strings.TrimFunc(name, jsSpace)
		value = strings.TrimFunc(value, jsSpace)
		if value == "" {
			subsection = name
			continue
		}
		if subsection != "" && strings.TrimLeftFunc(line, jsSpace) == line {
			subsection = ""
		}
		if subsection != "" {
			name = subsection + "." + name
		}
		if result[section] == nil {
			result[section] = map[string]string{}
		}
		if _, found := result[section][name]; !found {
			orders[section] = append(orders[section], name)
		}
		result[section][name] = value
	}
	return result, orders, nil
}

func hasEnvironment(env map[string]string, key string) bool { _, exists := env[key]; return exists }

// NativeAWSReadFile is an explicit physical primitive; callers still supply
// their actual frozen private/config home, rather than an implicit os.UserHomeDir.
func NativeAWSReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func previousSpace(prefix string) bool {
	last, _ := utf8.DecodeLastRuneInString(prefix)
	return jsSpace(last)
}
