package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/environmentwatch"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"path/filepath"
)

func (m *MainPrint) startNativeEnvironmentWatcher(scope *cooperative.Scope) error {
	if !m.options.Native.NativeProcess || m.agentChild || m.environmentWatcher != nil || envTruthyNative(m.options.Native.Environment["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	future, err := nativeSkillWork(m, scope, func() (*environmentwatch.Watcher, error) {
		return environmentwatch.Start(environmentwatch.Options{CWD: m.workingDirectory(), Snapshot: func() (protocol.JSONObject, error) {
			value, err := m.hook.Snapshot()
			if err != nil {
				return protocol.JSONObject{}, err
			}
			return value.Clone(), nil
		}, Event: m.nativeFileChanged, Error: func(err error) { m.environmentHookNotice(err.Error(), true) }})
	})
	if err != nil {
		return err
	}
	m.environmentWatcher, err = future.Await(scope)
	return err
}

func (m *MainPrint) updateNativeEnvironmentWatchPaths(scope *cooperative.Scope, raw []json.RawMessage) error {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.environmentWatcher == nil {
		return nil
	} // Original updateWatchPaths ignores pre-initialization calls.
	paths := make([]string, len(raw))
	for i, path := range raw {
		if err := json.Unmarshal(path, &paths[i]); err != nil {
			return err
		}
	}
	watcher := m.environmentWatcher
	future, err := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, watcher.Update(paths) })
	if err != nil {
		return err
	}
	_, err = future.Await(scope)
	return err
}

func (m *MainPrint) environmentHookNotice(text string, isError bool) {
	if m.options.OnEnvironmentHookMessage != nil {
		m.options.OnEnvironmentHookMessage(text, isError)
	}
}

func (m *MainPrint) nativeFileChanged(ctx context.Context, path, event string) error {
	receipt, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		if ctx.Err() != nil || m.shuttingDown || m.closed {
			return nil
		}
		fields := protocol.NewJSONObject(protocol.JSONField{Name: "permission_mode", Value: nil}, protocol.JSONField{Name: "agent_id", Value: nil}, protocol.JSONField{Name: "file_path", Value: quoteMain(path)}, protocol.JSONField{Name: "event", Value: quoteMain(event)})
		results, err := m.hook.ExecuteOutside(ctx, queryhooks.OutsideInput{Event: "FileChanged", SessionID: m.Controller.SessionID(), MatchQuery: filepath.Base(path), Fields: fields, Call: queryhooks.Context{Scope: scope}})
		if err != nil {
			return err
		}
		if len(results) > 0 {
			m.hookEnvironment.Invalidate()
		}
		paths := []json.RawMessage{}
		for _, result := range results {
			if result.WatchPaths != nil {
				var rows []json.RawMessage
				if err = json.Unmarshal(result.WatchPaths, &rows); err != nil {
					return err
				}
				paths = append(paths, rows...)
			}
			if text := hookResultString(result.SystemMessage); text != "" {
				m.environmentHookNotice(text, false)
			}
			if !result.Succeeded {
				if text := hookResultString(result.Output); text != "" {
					m.environmentHookNotice(text, true)
				}
			}
		}
		if len(paths) > 0 {
			return m.updateNativeEnvironmentWatchPaths(scope, paths)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}

func hookResultString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
