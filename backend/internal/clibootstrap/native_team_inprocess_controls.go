package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

func (m *MainPrint) InterruptTeammateWork(ctx context.Context, id string) (bool, error) {
	return inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (bool, error) {
		return m.Controller.InterruptInProcessWorkOnOwner(scope, id)
	})
}
func (m *MainPrint) SendTeammateUserMessage(ctx context.Context, id, message string) error {
	_, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
		return struct{}{}, m.Controller.InjectInProcessUserMessageOnOwner(scope, id, message)
	})
	return err
}
func (m *MainPrint) SetTeammatePermissionMode(ctx context.Context, id, mode string) error {
	_, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
		return struct{}{}, m.Controller.SetInProcessPermissionModeOnOwner(scope, id, mode)
	})
	return err
}
func (m *MainPrint) appendInProcessInterrupt(ctx context.Context, id string) error {
	_, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
		message, err := m.factory.CreateOrderedAssistantAPIError(storage.Object{"content": quoteMain(messages.Interrupt)})
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			return teamspawn.AppendMessage(task, message)
		})
	})
	return err
}
