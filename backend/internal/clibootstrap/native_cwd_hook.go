package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func (m *MainPrint) nativeCwdChanged(scope *cooperative.Scope, oldCwd, newCwd string) error {
	if oldCwd == newCwd || !m.options.Native.NativeProcess || m.hook == nil {
		return nil
	}
	snapshot, err := m.hook.Snapshot()
	if err != nil {
		return err
	}
	has := false
	for _, event := range []string{"CwdChanged", "FileChanged"} {
		var rows []json.RawMessage
		if raw := snapshot.Get(event); raw != nil {
			if err = json.Unmarshal(raw, &rows); err != nil {
				return err
			}
			has = has || len(rows) > 0
		}
	}
	if !has {
		return nil
	}
	sid := m.Controller.SessionID()
	future, err := nativeSkillWork(m, scope, func() (struct{}, error) { m.hookEnvironment.ClearCwd(sid); return struct{}{}, nil })
	if err != nil {
		return err
	}
	if _, err = future.Await(scope); err != nil {
		return err
	}
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "permission_mode", Value: nil}, protocol.JSONField{Name: "agent_id", Value: nil}, protocol.JSONField{Name: "cwd", Value: quoteMain(newCwd)}, protocol.JSONField{Name: "old_cwd", Value: quoteMain(oldCwd)}, protocol.JSONField{Name: "new_cwd", Value: quoteMain(newCwd)})
	results, failure := m.hook.ExecuteOutside(context.Background(), queryhooks.OutsideInput{Event: "CwdChanged", SessionID: sid, Fields: fields, Call: queryhooks.Context{Scope: scope}})
	paths := []string{}
	if failure != nil {
		m.environmentHookNotice(failure.Error(), true)
	} else {
		if len(results) > 0 {
			m.hookEnvironment.Invalidate()
		}
		for _, result := range results {
			if result.WatchPaths != nil {
				var rows []string
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
	}
	if m.environmentWatcher != nil {
		watcher := m.environmentWatcher
		update, err := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, watcher.CwdChanged(newCwd, paths) })
		if err != nil {
			return err
		}
		_, err = update.Await(scope)
		return err
	}
	return nil
}
