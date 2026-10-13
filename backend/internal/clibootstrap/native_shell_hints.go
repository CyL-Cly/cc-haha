package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/claudecodehints"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type NativePluginHint struct {
	ID                                            json.RawMessage
	Name, Marketplace, Description, SourceCommand string
}

func cloneNativeHint(h *NativePluginHint) *NativePluginHint {
	if h == nil {
		return nil
	}
	copy := *h
	copy.ID = append(json.RawMessage{}, h.ID...)
	return &copy
}

type nativeHintRuntime struct {
	// The feature-evaluation primitive defaults to the frozen public release's
	// actual GrowthBook override rules; tests may supply a resolved gate.
	featureEnabled func(map[string]string, protocol.JSONObject) bool
	store          claudecodehints.Store
	recommendation *NativePluginHint
	listeners      map[uint64]func(*NativePluginHint)
	next           uint64
	checking       bool
	resolver       *pluginsapi.MarketplaceResolver
}

func (m *MainPrint) nativeHintRuntimeOnOwner() *nativeHintRuntime {
	if m.nativeHints == nil {
		m.nativeHints = &nativeHintRuntime{listeners: map[uint64]func(*NativePluginHint){}}
	}
	return m.nativeHints
}
func (m *MainPrint) nativeShellHintsBindings() *builtintools.ShellHintBindings {
	return &builtintools.ShellHintBindings{AgentID: m.options.AgentID, Record: m.recordNativePluginHint}
}
func (m *MainPrint) recordNativePluginHint(scope *cooperative.Scope, hint claudecodehints.Hint) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	runtime := m.nativeHintRuntimeOnOwner()
	if runtime.store.Shown() {
		return nil
	}
	settings, n := m.nativeSettings, m.options.Native
	featureEnabled := runtime.featureEnabled
	if featureEnabled == nil {
		featureEnabled = claudecodehints.Enabled
	}
	if settings == nil || n == nil || m.agentChild || m.teamWorker != nil {
		return nil
	}
	work, e := nativeSkillWork(m, scope, func() (claudecodehints.GateState, error) {
		global, e := m.Global.Get()
		if e != nil {
			return claudecodehints.GateState{}, e
		}
		state := claudecodehints.StateFromGlobal(*global)
		state.Enabled = featureEnabled(n.Environment, *global)
		if !state.Enabled || state.Disabled || len(state.Shown) >= 100 {
			return state, nil
		}
		id := nativeHintString(hint.Value)
		installed, e := settings.plugins.installed.Session(context.Background())
		if e != nil {
			return state, e
		}
		entries := nativeInProcessObject(installed.Get("plugins"))
		var installs []protocol.JSONObject
		_ = json.Unmarshal(entries.Get(id), &installs)
		for _, entry := range installs {
			if entry.String("scope") == "user" || entry.String("scope") == "managed" || entry.String("projectPath") == n.CWD {
				merged, e := settings.startup.GetInitialSettings()
				if e != nil {
					return state, e
				}
				enabled := nativeInProcessObject(merged.Get("enabledPlugins"))
				state.Installed = enabled.Get(id) != nil
				break
			}
		}
		policy, e := settings.startup.GetSettingsForSource("policySettings")
		if e != nil {
			return state, e
		}
		if policy != nil {
			enabled := nativeInProcessObject(policy.Get("enabledPlugins"))
			state.Blocked = string(enabled.Get(id)) == "false"
		}
		return state, nil
	})
	if e != nil {
		return e
	}
	gate, e := work.Await(scope)
	if e != nil {
		return e
	}
	if runtime.store.Record(hint, gate) {
		return m.resolveNativeHintOnOwner(scope)
	}
	return nil
}
func nativeHintString(raw json.RawMessage) string {
	u, _ := protocol.DecodeJavaScriptJSONString(raw)
	return string(utf16.Decode(u))
}
func (m *MainPrint) resolveNativeHintOnOwner(scope *cooperative.Scope) error {
	runtime := m.nativeHintRuntimeOnOwner()
	// Source's React hook is absent in headless SDKs and remote terminals.
	if len(runtime.listeners) == 0 || runtime.checking || runtime.recommendation != nil {
		return nil
	}
	hint, sequence := runtime.store.Pending()
	if hint == nil {
		return nil
	}
	runtime.checking = true
	settings, n := m.nativeSettings, m.options.Native
	resolver := runtime.resolver
	type resolved struct {
		resolver *pluginsapi.MarketplaceResolver
		hint     *NativePluginHint
	}
	work, e := nativeSkillWork(m, scope, func() (resolved, error) {
		if resolver == nil {
			tls, e := tlsconfig.NewState(n.readFile)
			if e != nil {
				return resolved{}, e
			}
			client, e := webfetch.NewHTTPClient(n.Environment, tls)
			if e != nil {
				return resolved{}, e
			}
			resolver, e = pluginsapi.NewMarketplaceResolver(pluginsapi.MarketplaceOptions{Home: n.Home, WorkDir: n.CWD, PluginsDir: settings.plugins.directories.Primary, Environment: n.Environment, Store: settings.store, Now: n.Now, Client: client})
			if e != nil {
				return resolved{}, e
			}
		}
		plugin, e := resolver.Lookup(context.Background(), nativeHintString(hint.Value))
		if e != nil || plugin == nil {
			return resolved{resolver: resolver}, nil
		}
		_, market := claudecodehints.Identifier(hint.Value)
		return resolved{resolver, &NativePluginHint{append(json.RawMessage{}, hint.Value...), plugin.Entry.String("name"), market, plugin.Entry.String("description"), nativeHintString(hint.SourceCommand)}}, nil
	})
	if e != nil {
		runtime.checking = false
		return e
	}
	_, e = work.Then(scope, func(current *cooperative.Scope, result resolved, failure error) error {
		runtime.checking = false
		runtime.resolver = result.resolver
		if failure == nil && result.hint != nil {
			runtime.store.MarkShown()
			runtime.recommendation = cloneNativeHint(result.hint)
			for _, listener := range runtime.listeners {
				listener(cloneNativeHint(result.hint))
			}
		}
		runtime.store.Clear(sequence)
		return nil
	})
	return e
}

func (m *MainPrint) SubscribePluginHints(ctx context.Context, listener func(*NativePluginHint)) (*NativePluginHint, func(), error) {
	var initial *NativePluginHint
	var id uint64
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		runtime := m.nativeHintRuntimeOnOwner()
		initial = cloneNativeHint(runtime.recommendation)
		if listener != nil {
			runtime.next++
			id = runtime.next
			runtime.listeners[id] = listener
		}
		return m.resolveNativeHintOnOwner(scope)
	})
	if e == nil {
		e = receipt.Wait()
	}
	if e != nil {
		return nil, nil, e
	}
	var once sync.Once
	return initial, func() {
		once.Do(func() {
			receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
				if m.nativeHints != nil {
					delete(m.nativeHints.listeners, id)
				}
				return nil
			})
			if e == nil {
				_ = receipt.Wait()
			}
		})
	}, nil
}
func (m *MainPrint) RespondPluginHint(ctx context.Context, response string) (string, error) {
	if response != "yes" && response != "no" && response != "disable" {
		return "", errors.New("Unknown plugin recommendation response")
	}
	var notice string
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		runtime := m.nativeHintRuntimeOnOwner()
		hint := cloneNativeHint(runtime.recommendation)
		resolver := runtime.resolver
		if hint == nil {
			return nil
		}
		work, e := nativeSkillWork(m, scope, func() (string, error) {
			if e := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := claudecodehints.Respond(*current, hint.ID, response == "disable")
				return &next, nil
			}); e != nil {
				return "", e
			}
			if response != "yes" {
				return "", nil
			}
			// The actual cache/fetch producer is checked again at confirmation,
			// then the shared CLI materializer installs at Source's user scope.
			plugin, e := resolver.Lookup(ctx, nativeHintString(hint.ID))
			if e != nil || plugin == nil {
				return "Failed to install " + hint.Name, nil
			}
			jobs, e := newPluginCLIJobs(ctx, m.options.Native)
			if e != nil {
				return "", e
			}
			defer jobs.Join()
			operations, e := m.nativeSettings.pluginCLIOperations(m.pluginOptions, jobs)
			if e != nil {
				return "", e
			}
			_, e = operations.Install(ctx, nativeHintString(hint.ID), "user")
			if e != nil {
				return "Failed to install " + hint.Name, nil
			}
			return "✓ " + hint.Name + " installed · restart to apply", nil
		})
		if e != nil {
			return e
		}
		notice, e = work.Await(scope)
		if e != nil {
			return e
		}
		runtime.recommendation = nil
		for _, listener := range runtime.listeners {
			listener(nil)
		}
		return m.resolveNativeHintOnOwner(scope)
	})
	if e == nil {
		e = receipt.Wait()
	}
	return notice, e
}
