package clibootstrap

import (
	"context"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func (m *MainPrint) nativeVertexBinding(scope *cooperative.Scope) (*requestretry.NativeVertex, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	env := m.nativeOpenAIEnvironment()
	if !envTruthyNative(env["CLAUDE_CODE_USE_VERTEX"]) || envTruthyNative(env["CLAUDE_CODE_USE_BEDROCK"]) {
		return nil, nil
	}
	if !m.options.Native.NativeProcess || m.gcpCredentials == nil {
		return nil, cooperative.ErrRestricted
	}
	return requestretry.NewNativeVertex(m.Owner, scope, func(ctx context.Context, model string) (requestretry.NativeVertexClient, error) {
		env := m.nativeOpenAIEnvironment()
		region := cloudauth.VertexRegion(model, env)
		base := strings.TrimSpace(env["ANTHROPIC_VERTEX_BASE_URL"])
		if base == "" {
			base = "https://" + region + "-aiplatform.googleapis.com/v1"
			if region == "global" {
				base = "https://aiplatform.googleapis.com/v1"
			}
		}
		if _, err := cloudauth.ParseEndpoint(base); err != nil {
			return requestretry.NativeVertexClient{}, err
		}
		config := &provider.VertexConfig{Region: region, ProjectID: env["ANTHROPIC_VERTEX_PROJECT_ID"]}
		if envTruthyNative(env["CLAUDE_CODE_SKIP_VERTEX_AUTH"]) {
			return requestretry.NativeVertexClient{BaseURL: base, Config: config}, nil
		}
		settings, err := m.Startup.GetInitialSettings()
		if err != nil {
			return requestretry.NativeVertexClient{}, err
		}
		if settings.String("gcpAuthRefresh") != "" {
			if err = m.nativeRefreshGCPCredentials(ctx, scope); err != nil {
				return requestretry.NativeVertexClient{}, err
			}
		}
		auth, err := m.nativeGoogleAuth(scope, env)
		if err != nil {
			return requestretry.NativeVertexClient{}, err
		}
		// Source starts getClient's ADC promise in the wrapper constructor, before
		// prepareOptions asks for headers. Keep this separate from the token request.
		client, err := auth.GetClient(ctx)
		if err != nil {
			return requestretry.NativeVertexClient{}, err
		}
		config.Headers = func(ctx context.Context) (http.Header, string, error) {
			if !scope.BelongsTo(m.Owner) {
				return nil, "", cooperative.ErrRestricted
			}
			headers, err := client.GetRequestHeaders(ctx)
			return headers, client.ProjectID, err
		}
		return requestretry.NativeVertexClient{BaseURL: base, Config: config}, nil
	}, func(model string) (string, error) {
		if !scope.BelongsTo(m.Owner) {
			return "", cooperative.ErrRestricted
		}
		return (modelaccounting.RestoredModelResolver{Environment: m.nativeOpenAIEnvironment(), Settings: m.Startup.GetInitialSettings}).Canonical(model)
	})
}
func (m *MainPrint) nativeGoogleAuth(scope *cooperative.Scope, env map[string]string) (*cloudauth.GoogleAuth, error) {
	fallback := ""
	hasProject := env["GCLOUD_PROJECT"] != "" || env["GOOGLE_CLOUD_PROJECT"] != "" || env["gcloud_project"] != "" || env["google_cloud_project"] != ""
	hasKey := env["GOOGLE_APPLICATION_CREDENTIALS"] != "" || env["google_application_credentials"] != ""
	if !hasProject && !hasKey {
		fallback = env["ANTHROPIC_VERTEX_PROJECT_ID"]
	}
	return cloudauth.NewGoogleAuth(cloudauth.GoogleOptions{Environment: env, Home: m.options.Native.Home, CWD: m.state.Snapshot().CWD, Platform: m.options.Platform, ProjectFallback: fallback, Now: m.options.Native.Now, Do: func(request *http.Request) (*http.Response, error) { return m.nativeCloudHTTP(scope, request) }, ReadFile: func(ctx context.Context, path string) ([]byte, error) {
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
	}})
}
