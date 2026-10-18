package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) terminalExternalIncludes(scope *cooperative.Scope) ([]protocol.JSONObject, bool, error) {
	future, err := nativeSkillWork(m, scope, m.userContext.ExternalMemoryFilesForInspection)
	if err != nil {
		return nil, false, err
	}
	files, err := future.Await(scope)
	if err != nil {
		return nil, false, err
	}
	rows := []protocol.JSONObject{}
	for _, file := range files {
		within := builtintools.PathInWorkingPath(file.Path, m.state.Snapshot().OriginalCWD)
		if file.Type != "User" && file.Parent != "" && !within {
			rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "path", Value: quoteMain(file.Path)}, protocol.JSONField{Name: "parent", Value: quoteMain(file.Parent)}))
		}
	}
	key, err := m.originalProjectConfigKey(scope)
	if err != nil {
		return nil, false, err
	}
	project, err := m.Global.GetProject(key)
	if err != nil {
		return nil, false, err
	}
	return rows, string(project.Get("hasClaudeMdExternalIncludesApproved")) == "true", nil
}

func (m *MainPrint) SetTerminalExternalIncludes(ctx context.Context, approved bool) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		key, err := m.originalProjectConfigKey(scope)
		if err != nil {
			return err
		}
		if err := m.Global.SaveProject(key, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("hasClaudeMdExternalIncludesApproved", rawMain(approved))
			next.Set("hasClaudeMdExternalIncludesWarningShown", rawMain(true))
			return &next, nil
		}); err != nil {
			return err
		}
		m.userContext.SetExternalIncludesApproved(approved)
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}

func (m *MainPrint) restoreTerminalExternalIncludes(scope *cooperative.Scope, snapshot protocol.JSONObject) error {
	key, err := m.originalProjectConfigKey(scope)
	if err != nil {
		return err
	}
	global := nativeInProcessObject(snapshot.Get("global"))
	projects := nativeInProcessObject(global.Get("projects"))
	project := nativeInProcessObject(projects.Get(key))
	if err := m.Global.SaveProject(key, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		for _, field := range []string{"hasClaudeMdExternalIncludesApproved", "hasClaudeMdExternalIncludesWarningShown"} {
			next.Set(field, project.Get(field))
		}
		return &next, nil
	}); err != nil {
		return err
	}
	m.userContext.SetExternalIncludesApproved(string(project.Get("hasClaudeMdExternalIncludesApproved")) == "true")
	return nil
}
