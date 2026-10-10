package clibootstrap

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/httphook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func (m *MainPrint) nativeHTTPHookBindings() *queryhooks.HTTPBindings {
	if !m.options.Native.NativeProcess {
		return nil
	}
	return &queryhooks.HTTPBindings{Owner: m.Owner, Run: func(ctx context.Context, call queryhooks.Context, hook, input protocol.JSONObject) (httphook.Result, error) {
		if call.Scope == nil || !call.Scope.BelongsTo(m.Owner) {
			return httphook.Result{}, cooperative.ErrRestricted
		}
		settings, err := m.Startup.GetInitialSettings()
		if err != nil {
			return httphook.Result{}, err
		}
		env := map[string]string{}
		for _, field := range m.environment.Snapshot().OwnProperties() {
			var name, value string
			_ = json.Unmarshal(field.NameJSON, &name)
			_ = json.Unmarshal(field.Value, &value)
			env[name] = value
		}
		prepared, err := httphook.Prepare(hook, *settings, env)
		if err != nil {
			return httphook.Result{}, err
		}
		if prepared.Blocked != nil {
			return httphook.Result{Body: quoteMain(""), Error: prepared.Blocked}, nil
		}
		var sandboxProxy *url.URL
		// Like Source getSandboxProxyConfig, an uninitialized pre-CLI
		// manager has no proxy even when the setting is already enabled.
		if runtime := m.sandbox; runtime != nil && runtime.manager != nil {
			port, _ := runtime.manager.ProxyPorts()
			sandboxProxy, _ = url.Parse("http://127.0.0.1:" + strconv.Itoa(int(port)))
		}
		settingsSnapshot := settings.Clone()
		hookSnapshot := hook.Clone()
		body := rawMain(input)
		future, err := nativeSkillWork(m, call.Scope, func() (httphook.Result, error) {
			options := httphook.NativeOptions{Environment: env, SandboxProxy: sandboxProxy}
			proxy := provider.ResolveNativeProxySelection(hookSnapshot.String("url"), env)
			if strings.HasPrefix(strings.ToLower(hookSnapshot.String("url")), "https:") || !proxy.Bypass && strings.HasPrefix(strings.ToLower(proxy.ProxyURL), "https:") {
				if m.tls == nil {
					return httphook.Result{}, &httphook.CapabilityError{Boundary: "original TLS configuration state"}
				}
				tlsConfig, tlsError := m.tls.VerifiedConfig(env)
				if tlsError != nil {
					return httphook.Result{}, tlsError
				}
				options.VerifiedTLS = tlsConfig
			}
			return httphook.Execute(ctx, hookSnapshot, settingsSnapshot, body, options)
		})
		if err != nil {
			return httphook.Result{}, err
		}
		return future.Await(call.Scope)
	}}
}
