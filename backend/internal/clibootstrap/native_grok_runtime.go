package clibootstrap

import (
	"context"
	"path/filepath"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/grokauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func (m *MainPrint) nativeGrokHasAuth(ctx context.Context, scope *cooperative.Scope) (bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return false, cooperative.ErrRestricted
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path := protocol.TrimJavaScriptString(m.nativeOpenAIEnvironment()["GROK_OAUTH_FILE"])
	if path == "" {
		return false, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.workingDirectory(), path)
	}
	var data []byte
	var readError error
	if err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { data, readError = m.options.Native.readFile(path); return nil }); err != nil {
		return false, err
	}
	if readError != nil {
		return false, nil
	}
	tokens := grokauth.NormalizeFile(data)
	return tokens != nil, nil
}
func (m *MainPrint) nativeGrokConfig(scope *cooperative.Scope) *provider.GrokOAuthConfig {
	if !m.options.Native.NativeProcess {
		return nil
	}
	return &provider.GrokOAuthConfig{Platform: m.options.Platform, Arch: runtime.GOARCH, NowMillis: func() int64 { return int64(m.options.Native.NowMillis()) }, RuntimeCatalog: providerproxy.GrokModelCatalog,
		HasAuth: func(ctx context.Context) (bool, error) { return m.nativeGrokHasAuth(context.WithoutCancel(ctx), scope) }, FreshTokens: func(ctx context.Context, force bool) (*protocol.JSONObject, error) {
			return m.nativeGrokImageTokens(context.WithoutCancel(ctx), scope, force)
		}}
}
