package clibootstrap

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"slices"
)

func (m *MainPrint) ValidateTerminalDirectory(ctx context.Context, path string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.nativePermissionStartup == nil {
			return cooperative.ErrClosed
		}
		current, err := m.Host.NativePermissionContext(scope)
		if err != nil {
			return err
		}
		location := m.state.Snapshot()
		pending, err := nativeSkillWork(m, scope, func() (protocol.JSONObject, error) {
			return m.nativePermissionStartup.ValidateWorkspaceDirectoryAt(path, current, location.CWD, location.OriginalCWD)
		})
		if err != nil {
			return err
		}
		result, err = pending.Await(scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
func (m *MainPrint) AddTerminalDirectory(ctx context.Context, path string, remember bool) (protocol.JSONObject, error) {
	// The actual Source form validates before publishing its choice callback.
	// Confirmation uses that validated absolute path without a second stat.
	absolute := path
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		destination := "session"
		if remember {
			destination = "localSettings"
		}
		update := rawMain([]any{map[string]any{"type": "addDirectories", "directories": []string{absolute}, "destination": destination}})
		persistenceError := m.Host.ApplyNativePermissionUpdates(scope, update)
		current, err := m.Host.NativePermissionContext(scope)
		if err != nil {
			return err
		}
		directories := sdkpermissions.WorkspaceDirectoryEntries(current)
		present := false
		for _, directory := range directories {
			present = present || directory.String("path") == absolute
		}
		if !present {
			if persistenceError != nil {
				return persistenceError
			}
			return errors.New("Directory permission was not applied")
		}
		// Bootstrap instruction dirs contain explicit CLI/interactive additions,
		// rather than every persisted permission or PWD alias from startup.
		if !slices.Contains(m.nativePermissionInitial.InstructionDirectories, absolute) {
			m.nativePermissionInitial.InstructionDirectories = append(m.nativePermissionInitial.InstructionDirectories, absolute)
		}
		if !slices.Contains(m.options.Arguments.AdditionalDirectories, absolute) {
			m.options.Arguments.AdditionalDirectories = append(m.options.Arguments.AdditionalDirectories, absolute)
		}
		if m.userContext != nil {
			m.userContext.SetAdditionalInstructionDirectories(m.nativeInstructionDirectories())
		}
		roots := []string{}
		for _, directory := range directories {
			roots = append(roots, directory.String("path"))
		}
		if m.prompt != nil {
			m.prompt.SetAdditionalWorkingDirectories(roots)
		}
		if err = m.refreshNativeSandbox(scope); err != nil {
			return err
		}
		if err = m.Host.RefreshNativeToolCatalog(scope); err != nil {
			return err
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "resultType", Value: quoteMain("success")}, protocol.JSONField{Name: "absolutePath", Value: quoteMain(absolute)}, protocol.JSONField{Name: "remember", Value: rawMain(remember)})
		if persistenceError != nil {
			result.Set("persistenceError", quoteMain(persistenceError.Error()))
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

// The /permissions Workspace callbacks use the existing permission authority.
// Source does not update the separate CLAUDE.md bootstrap or eagerly refresh
// sandbox inputs here, unlike the /add-dir command's callback.
func (m *MainPrint) TerminalWorkspaceDirectory(ctx context.Context, path string, remove, remember bool) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		kind, destination := "addDirectories", "session"
		if remove {
			kind = "removeDirectories"
		} else if remember {
			destination = "localSettings"
		}
		err := m.Host.ApplyNativePermissionUpdates(scope, rawMain([]any{map[string]any{"type": kind, "directories": []string{path}, "destination": destination}}))
		current, snapshotErr := m.Host.NativePermissionContext(scope)
		if snapshotErr != nil {
			return snapshotErr
		}
		roots := []string{}
		for _, directory := range sdkpermissions.WorkspaceDirectoryEntries(current) {
			roots = append(roots, directory.String("path"))
		}
		if m.prompt != nil {
			m.prompt.SetAdditionalWorkingDirectories(roots)
		}
		if refreshErr := m.Host.RefreshNativeToolCatalog(scope); refreshErr != nil {
			return refreshErr
		}
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
