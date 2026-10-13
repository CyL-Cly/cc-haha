package clibootstrap

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/authhelper"
	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func (m *MainPrint) nativeBedrockBinding(scope *cooperative.Scope) (*requestretry.NativeBedrock, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if !envTruthyNative(m.nativeOpenAIEnvironment()["CLAUDE_CODE_USE_BEDROCK"]) {
		return nil, nil
	}
	if !m.options.Native.NativeProcess || m.awsCredentials == nil {
		return nil, cooperative.ErrRestricted
	}
	return requestretry.NewNativeBedrock(m.Owner, scope, func(ctx context.Context, model string) (requestretry.NativeBedrockClient, error) {
		env := m.nativeOpenAIEnvironment()
		settings, err := m.Startup.GetInitialSettings()
		if err != nil {
			return requestretry.NativeBedrockClient{}, err
		}
		selector, err := modelselection.NewDirectCredentials(modelselection.Options{Environment: env, Settings: settings.Clone()})
		if err != nil {
			return requestretry.NativeBedrockClient{}, err
		}
		region := env["AWS_REGION"]
		if region == "" {
			region = env["AWS_DEFAULT_REGION"]
		}
		if region == "" {
			region = "us-east-1"
		}
		if model == selector.SmallFastModel() && env["ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION"] != "" {
			region = env["ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION"]
		}
		base := strings.TrimSpace(env["ANTHROPIC_BEDROCK_BASE_URL"])
		if base == "" {
			base = "https://bedrock-runtime." + region + ".amazonaws.com"
		}
		if _, err = cloudauth.ParseEndpoint(base); err != nil {
			return requestretry.NativeBedrockClient{}, err
		}
		result := requestretry.NativeBedrockClient{BaseURL: base, BearerToken: env["AWS_BEARER_TOKEN_BEDROCK"]}
		config := &provider.BedrockConfig{Region: region, SkipAuth: envTruthyNative(env["CLAUDE_CODE_SKIP_BEDROCK_AUTH"]) || result.BearerToken != "", Now: m.options.Native.Now}
		result.Config = config
		if config.SkipAuth {
			return result, nil
		}
		exported, err := m.nativeAWSExportedCredentials(ctx, scope)
		if err != nil {
			return result, err
		}
		config.Credentials = func(ctx context.Context) (cloudauth.AWSCredentials, error) {
			if !scope.BelongsTo(m.Owner) {
				return cloudauth.AWSCredentials{}, cooperative.ErrRestricted
			}
			if exported != nil {
				return *exported, nil
			}
			resolver, err := m.nativeAWSResolver(scope)
			if err != nil {
				return cloudauth.AWSCredentials{}, err
			}
			return resolver.Resolve(ctx)
		}
		return result, nil
	}, func(model string) (string, error) {
		if !scope.BelongsTo(m.Owner) {
			return "", cooperative.ErrRestricted
		}
		return (modelaccounting.RestoredModelResolver{Environment: m.nativeOpenAIEnvironment(), Settings: m.Startup.GetInitialSettings}).Canonical(model)
	})
}

func (m *MainPrint) nativeAWSResolver(scope *cooperative.Scope) (*cloudauth.AWSResolver, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	env := m.nativeOpenAIEnvironment()
	region := env["AWS_REGION"]
	if region == "" {
		region = "us-east-1"
	}
	sts := &cloudauth.STS{Region: region, Endpoint: env["AWS_ENDPOINT_URL_STS"], Now: m.options.Native.Now, Do: func(request *http.Request) (*http.Response, error) { return m.nativeCloudHTTP(scope, request) }}
	return cloudauth.NewAWSResolver(cloudauth.AWSOptions{Environment: env, Home: m.options.Native.Home, STS: sts, Metadata: &cloudauth.Metadata{Do: func(request *http.Request) (*http.Response, error) { return m.nativeCloudHTTP(scope, request) }, Sleep: func(ctx context.Context, duration time.Duration) error {
		future, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-timer.C:
				return struct{}{}, nil
			case <-ctx.Done():
				return struct{}{}, ctx.Err()
			}
		})
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		return err
	}}, SSO: &cloudauth.SSO{State: m.awsSSOState, RoleEndpoint: env["AWS_ENDPOINT_URL_SSO"], OIDCEndpoint: env["AWS_ENDPOINT_URL_SSO_OIDC"], Do: func(request *http.Request) (*http.Response, error) { return m.nativeCloudHTTP(scope, request) }}, WriteFile: func(ctx context.Context, path string, raw []byte) error {
		future, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			if err := ctx.Err(); err != nil {
				return struct{}{}, err
			}
			if err := m.options.Native.check("writeFile", path); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, os.WriteFile(path, raw, 0666)
		})
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		return err
	}, ConfigReadFile: func(ctx context.Context, path string, ignore bool) ([]byte, error) {
		future, err := nativeSkillWork(m, scope, func() ([]byte, error) {
			return m.awsINIState.Read(ctx, path, ignore, func(ctx context.Context, path string) ([]byte, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return m.options.Native.readFile(path)
			})
		})
		if err != nil {
			return nil, err
		}
		return future.Await(scope)
	}, Now: m.options.Native.Now, ReadFile: func(ctx context.Context, path string) ([]byte, error) {
		future, err := nativeSkillWork(m, scope, func() ([]byte, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return m.options.Native.readFile(path)
		})
		if err != nil {
			return nil, err
		}
		return future.Await(scope)
	}, Run: func(ctx context.Context, command string, timeout time.Duration) (cloudauth.CommandOutput, error) {
		return m.nativeCloudCommand(ctx, scope, command, timeout, env)
	}})
}

func (m *MainPrint) nativeCloudCommand(ctx context.Context, scope *cooperative.Scope, command string, timeout time.Duration, env map[string]string) (cloudauth.CommandOutput, error) {
	cwd := m.state.Snapshot().CWD
	future, err := nativeSkillWork(m, scope, func() (cloudauth.CommandOutput, error) {
		if timeout > 0 {
			return cloudauth.RunAuthRefresh(ctx, command, timeout, cwd, env, m.options.CloudAuthStatus)
		}
		return cloudauth.RunConfiguredCommand(ctx, command, timeout, cwd, env)
	})
	if err != nil {
		return cloudauth.CommandOutput{}, err
	}
	return future.Await(scope)
}

func (m *MainPrint) nativeAWSExportedCredentials(ctx context.Context, scope *cooperative.Scope) (*cloudauth.AWSCredentials, error) {
	if !scope.BelongsTo(m.Owner) || m.awsCredentials == nil || !m.options.Native.NativeProcess || !envTruthyNative(m.nativeOpenAIEnvironment()["CLAUDE_CODE_USE_BEDROCK"]) {
		return nil, cooperative.ErrRestricted
	}
	proof, err := cooperative.NewNativeCloudHTTP(scope, m.Owner, "aws_credentials")
	if err != nil {
		return nil, err
	}
	lookup, err := m.awsCredentials.Begin(func(active context.Context) (json.RawMessage, error) {
		var value *cloudauth.AWSCredentials
		receipt, err := m.Owner.SubmitIndependent(active, cooperative.Read, func(callback *cooperative.Scope) error {
			return proof.Run(callback, active, "aws_credentials", func(next *cooperative.Scope) error {
				settings, err := m.Startup.GetInitialSettings()
				if err != nil {
					return err
				}
				refresh, export := settings.String("awsAuthRefresh"), settings.String("awsCredentialExport")
				projectRefresh, projectExport := false, false
				for _, source := range []string{"projectSettings", "localSettings"} {
					project, err := m.Startup.GetSettingsForSource(source)
					if err != nil {
						return err
					}
					if project != nil {
						projectRefresh = projectRefresh || authhelper.ConfiguredInSource(settings.Get("awsAuthRefresh"), project.Get("awsAuthRefresh"))
						projectExport = projectExport || authhelper.ConfiguredInSource(settings.Get("awsCredentialExport"), project.Get("awsCredentialExport"))
					}
				}
				trusted := false
				if (projectRefresh || projectExport) && m.options.Interactive {
					trusted, err = m.nativeGoalTrust(next)
					if err != nil {
						return err
					}
				}
				resolver, err := m.nativeAWSResolver(next)
				if err != nil {
					return err
				}
				env := m.nativeOpenAIEnvironment()
				value, err = cloudauth.RefreshAndExport(active, cloudauth.AWSHelperOptions{Refresh: refresh, Export: export, RefreshFromProject: projectRefresh, ExportFromProject: projectExport, Trusted: trusted, NonInteractive: !m.options.Interactive, Probe: func(ctx context.Context) error {
					credentials, err := resolver.Resolve(ctx)
					if err != nil {
						return err
					}
					region := env["AWS_REGION"]
					if region == "" {
						region = "us-east-1"
					}
					return (cloudauth.STS{Region: region, Endpoint: env["AWS_ENDPOINT_URL_STS"], Now: m.options.Native.Now, Do: func(request *http.Request) (*http.Response, error) { return m.nativeCloudHTTP(next, request) }}).CallerIdentity(ctx, credentials)
				}, Run: func(ctx context.Context, command string, timeout time.Duration) (cloudauth.CommandOutput, error) {
					return m.nativeCloudCommand(ctx, next, command, timeout, env)
				}, ClearINI: func(ctx context.Context) error { _, err := resolver.ResolveINI(ctx); return err }, Error: func(text string) { m.environmentHookNotice(text, true) }})
				return err
			})
		})
		if err != nil {
			return nil, err
		}
		if err = receipt.Wait(); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(value)
		return raw, err
	})
	if err != nil {
		return nil, err
	}
	future, err := nativeSkillWork(m, scope, func() (json.RawMessage, error) { return lookup.Wait(ctx) })
	if err != nil {
		return nil, err
	}
	raw, err := future.Await(scope)
	if err != nil {
		return nil, err
	}
	var value *cloudauth.AWSCredentials
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (m *MainPrint) nativeCloudHTTP(scope *cooperative.Scope, request *http.Request) (*http.Response, error) {
	if !scope.BelongsTo(m.Owner) || request == nil {
		return nil, cooperative.ErrRestricted
	}
	env := m.nativeOpenAIEnvironment()
	if err := tlsconfig.ValidateOriginalTLSOptions(env); err != nil {
		return nil, err
	}
	certificates, err := m.tls.Certificates(env)
	if err != nil {
		return nil, err
	}
	exchange, err := provider.NativeDirectHTTPExchangeFromEnvironment(request.URL.String(), env)
	if err != nil {
		return nil, err
	}
	if request.URL.Scheme == "https" {
		exchange.ResolveVerifiedTLS = func() (*tls.Config, error) { return tlsconfig.VerifiedConfigFromCertificates(certificates) }
	}
	driver, err := providernative.New(m.Owner, scope, nil)
	if err != nil {
		return nil, err
	}
	bodyIO, err := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
		return scope.AwaitBytes(cooperative.StreamNext, work)
	})
	if err != nil {
		return nil, err
	}
	response, err := driver.OpenFetchWithOptions(request, exchange, provider.NativeFetchOptions{Redirect: "follow", ResolveExchange: func(target *url.URL) (*provider.RawHTTP1Exchange, error) {
		selected := *exchange
		if target.Scheme == "https" {
			selected.ResolveVerifiedTLS = func() (*tls.Config, error) { return tlsconfig.VerifiedConfigFromCertificates(certificates) }
		} else {
			selected.VerifiedTLS, selected.ResolveVerifiedTLS = nil, nil
		}
		return &selected, nil
	}})
	if err != nil {
		return nil, err
	}
	body, err := bodyIO.WrapFetchBody(response.Response.Body)
	if err != nil {
		_ = response.Response.Body.Close()
		return nil, err
	}
	result := *response.Response
	result.Body = body
	return &result, nil
}
