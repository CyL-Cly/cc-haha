package clibootstrap

import (
	"context"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) nativeReadCountCloud(scope *cooperative.Scope, ctx context.Context, model string, env map[string]string, credentials nativeReadCountCredentials) (nativeReadCountCredentials, error) {
	if !envTruthyNative(env["CLAUDE_CODE_USE_VERTEX"]) || envTruthyNative(env["CLAUDE_CODE_USE_BEDROCK"]) {
		return credentials, nil
	}
	binding, err := m.nativeVertexBinding(scope)
	if err != nil {
		return credentials, err
	}
	if binding == nil {
		return credentials, cooperative.ErrRestricted
	}
	cloud, err := binding.Client(ctx, model)
	if err != nil {
		return credentials, err
	}
	if cloud.Config == nil {
		return credentials, cooperative.ErrRestricted
	}
	config := *cloud.Config
	if config.Headers != nil {
		// ADC may await its actual shared owner. Resolve it on that owner,
		// then move only immutable headers into the physical HTTP worker.
		headers, project, err := config.Headers(ctx)
		if err != nil {
			return credentials, err
		}
		headers = headers.Clone()
		config.Headers = func(context.Context) (http.Header, string, error) { return headers.Clone(), project, nil }
	}
	credentials.vertex, credentials.baseURL = &config, cloud.BaseURL
	return credentials, nil
}
