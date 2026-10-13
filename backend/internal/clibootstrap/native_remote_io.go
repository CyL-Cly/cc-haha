package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func (m *MainPrint) prepareNativeRemoteIO(ctx context.Context, cancel context.CancelCauseFunc) error {
	if m.options.Arguments.SDKURL == "" {
		return nil
	}
	if m.options.Interactive || !m.options.Native.NativeProcess {
		return mainCap("RemoteIO actual native print entrypoint")
	}
	var env map[string]string
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error { env = m.nativeOpenAIEnvironment(); return nil })
	if err != nil {
		return err
	}
	if err = receipt.Wait(); err != nil {
		return err
	}
	global, _ := m.Global.Get()
	globalValue := protocol.NewJSONObject()
	if global != nil {
		globalValue = *global
	}
	keepalive := remoteio.BridgeKeepAliveInterval(env, globalValue)
	m.options.SessionIngressAuth.UpdateEnvironment(env)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(request *http.Request) (*url.URL, error) {
		selection := provider.ResolveNativeProxySelection(request.URL.String(), env)
		if selection.ProxyURL == "" || selection.Bypass {
			return nil, nil
		}
		return url.Parse(selection.ProxyURL)
	}
	if err := tlsconfig.ValidateOriginalTLSOptions(env); err != nil {
		return err
	}
	certificates, err := m.tls.Certificates(env)
	if err != nil {
		return err
	}
	transport.TLSClientConfig, err = tlsconfig.VerifiedConfigFromCertificates(certificates)
	if err != nil {
		return err
	}
	remote, err := remoteio.New(ctx, remoteio.Options{
		URL: m.options.Arguments.SDKURL, SessionID: m.Controller.SessionID(), Environment: env, IngressAuth: m.options.SessionIngressAuth,
		HTTP: &http.Client{Transport: transport}, UserAgent: "claude-code/" + provider.SourceCompatibilityVersion,
		WebSocketHTTP: m.nativeRemoteHTTP,
		GlobalHTTP:    m.nativeRemoteHTTP,
		InitialInput:  m.options.Input, BridgeOutput: m.options.Output, Debug: m.errorLogSink != nil && m.errorLogSink.DebugMode(),
		BridgeKeepAlive: &keepalive,
		OnClose: func(code int, err error) {
			if remoteio.PermanentClose(code) || err != nil {
				if err == nil {
					err = errRemotePermanent
				}
				cancel(err)
			}
		},
	})
	if err != nil {
		return err
	}
	m.remoteIO = remote
	m.options.Input, m.options.Output = remote, remote
	// RemoteIO always sends can_use_tool requests over its SDK transport.
	m.options.Arguments.PermissionPromptToolName = "stdio"
	remote.Start()
	return nil
}

var errRemotePermanent = errors.New("remote SDK session permanently closed")

func (m *MainPrint) bindNativeRemotePersistence(scope *cooperative.Scope) error {
	if m.remoteIO == nil {
		return nil
	}
	return m.Controller.BindRemoteIO(scope, m.remoteIO)
}

func (m *MainPrint) applyNativeSDKEnvironment(scope *cooperative.Scope, variables protocol.JSONObject) error {
	if !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess {
		return cooperative.ErrRestricted
	}
	if !variables.IsObject() {
		return mainCap("SDK environment variables object")
	}
	changed := map[string]string{}
	for _, property := range variables.OwnProperties() {
		value, err := protocol.ExactJavaScriptStringJSON(property.Value)
		if err != nil {
			return err
		}
		if err = m.environment.SetProperty(property.NameJSON, value); err != nil {
			return err
		}
		var name, text string
		_ = json.Unmarshal(property.NameJSON, &name)
		_ = json.Unmarshal(value, &text)
		changed[name] = text
	}
	m.tls.Clear()
	if variables.Get("CLAUDE_CODE_OAUTH_TOKEN") != nil && m.openAIAuth != nil {
		if err := m.openAIAuth.ClearClaudeTokenCache(scope); err != nil {
			return err
		}
	}
	m.options.SessionIngressAuth.UpdateEnvironment(changed)
	if m.remoteIO != nil {
		m.remoteIO.UpdateEnvironment(changed)
	}
	if m.nativeIngress != nil {
		m.nativeIngress.SetAfterLastCompact(envTruthyNative(m.nativeOpenAIEnvironment()["CLAUDE_AFTER_LAST_COMPACT"]))
	}
	return m.Host.SetNativeRequestEnvironment(scope, m.nativeOpenAIEnvironment())
}

// Keep the original input reader visible to process cleanup rather than leave
// an inherited stdin read goroutine detached after a permanent remote close.
var _ io.Reader = (*remoteio.Service)(nil)
