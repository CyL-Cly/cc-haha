package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

type nativeTeamState struct{ snapshot protocol.JSONObject }

func (m *MainPrint) SetTeamRuntimeSnapshot(ctx context.Context, name string, createdAt json.RawMessage) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.Host == nil || !m.initialized || m.closed || m.options.Native == nil || m.agentChild || m.teamWorker != nil {
			return cooperative.ErrRestricted
		}
		config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: m.options.Native.ConfigHomeDirectory(), TaskStore: m.taskStore, PID: m.options.PID, Platform: m.options.Platform, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }})
		if err != nil {
			return err
		}
		future, err := nativeSkillWork(m, scope, func() (protocol.JSONObject, error) {
			return config.RuntimeSnapshot(name, createdAt, m.Controller.SessionID())
		})
		if err != nil {
			return err
		}
		snapshot, err := future.Await(scope)
		if err != nil {
			return err
		}
		if err = m.Controller.SetLeaderTeamOnOwner(scope, snapshot.String("teamName")); err != nil {
			return err
		}
		m.teamState = &nativeTeamState{snapshot: snapshot.Clone()}
		m.publishTerminalTeamFooter(scope)
		return nil
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}

func (m *MainPrint) SessionPermissionMode(ctx context.Context) (string, error) {
	var mode string
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.closed {
			return cooperative.ErrClosed
		}
		state, err := m.Host.NativePermissionContext(scope)
		if err != nil {
			return err
		}
		mode = state.String("mode")
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return mode, err
}
