package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

// Concrete interactive plugin settings producers enter this transition. The
// ticket captures the actual owned cache before their real write/cache clear;
// the existing Host proves the limited settings delta on a fresh pointer
// without replacing session rules or admitting unrelated producers.
func (m *MainPrint) terminalPluginCacheEdit(ctx context.Context, work func(context.Context, *pluginCLIJobs) (protocol.JSONObject, error)) (protocol.JSONObject, error) {
	return m.terminalPluginCacheProducer(ctx, "", "", "", work)
}

func (m *MainPrint) terminalPluginUninstallCacheEdit(ctx context.Context, pluginID string, work func(context.Context, *pluginCLIJobs) (protocol.JSONObject, error)) (protocol.JSONObject, error) {
	if !strings.Contains(pluginID, "@") {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	return m.terminalPluginCacheProducer(ctx, pluginID, "", "", work)
}

func (m *MainPrint) terminalPluginOptionsCacheEdit(ctx context.Context, id, server string, work func(context.Context, *pluginCLIJobs) (protocol.JSONObject, error)) (protocol.JSONObject, error) {
	if id == "" {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	return m.terminalPluginCacheProducer(ctx, "", id, server, work)
}

func (m *MainPrint) terminalPluginCacheProducer(ctx context.Context, uninstallID, optionsID, server string, work func(context.Context, *pluginCLIJobs) (protocol.JSONObject, error)) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || m.nativeSettings == nil || m.pluginOptions == nil || m.Host == nil {
			return cooperative.ErrClosed
		}
		var edit *sdkhost.NativePluginCacheEdit
		var err error
		if optionsID != "" {
			edit, err = m.Host.BeginNativePluginOptionsCacheEdit(scope, optionsID, server)
		} else if uninstallID == "" {
			edit, err = m.Host.BeginNativePluginCacheEdit(scope)
		} else {
			edit, err = m.Host.BeginNativePluginUninstallCacheEdit(scope, uninstallID)
		}
		if err != nil {
			return err
		}
		pending, err := nativeSkillWork(m, scope, func() (protocol.JSONObject, error) {
			if uninstallID != "" {
				m.nativeSettings.mu.Lock()
				bindings := m.nativeSettings.plugins
				m.nativeSettings.mu.Unlock()
				if bindings == nil || bindings.installed == nil {
					return protocol.JSONObject{}, cooperative.ErrRestricted
				}
				installed, err := bindings.installed.Session(ctx)
				if err != nil {
					return protocol.JSONObject{}, err
				}
				var entries []json.RawMessage
				if json.Unmarshal(mainObj(installed.Get("plugins")).Get(uninstallID), &entries) != nil || len(entries) == 0 {
					return protocol.JSONObject{}, cooperative.ErrRestricted
				}
			}
			jobs, err := newPluginCLIJobs(ctx, m.options.Native)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			defer jobs.Join()
			value, err := work(ctx, jobs)
			return value.Clone(), err
		})
		if err != nil {
			return err
		}
		result, err = pending.Await(scope)
		return errors.Join(err, m.Host.CompleteNativePluginCacheEdit(scope, edit))
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
