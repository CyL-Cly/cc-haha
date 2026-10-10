package clibootstrap

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) SubscribeTeammateTasks(ctx context.Context, callback func(protocol.JSONObject)) (protocol.JSONObject, func(), error) {
	var value protocol.JSONObject
	var remove func(*cooperative.Scope) error
	_, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
		var err error
		value, remove, err = m.Controller.SubscribeBackgroundTasksOnOwner(scope, callback)
		return struct{}{}, err
	})
	if err != nil {
		return protocol.JSONObject{}, nil, err
	}
	var once sync.Once
	return value, func() {
		once.Do(func() {
			_, _ = inProcessParentCall(context.Background(), m, func(scope *cooperative.Scope) (struct{}, error) { return struct{}{}, remove(scope) })
		})
	}, nil
}
func (m *MainPrint) SetTeammateView(ctx context.Context, id string) error {
	_, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
		if id == "" {
			return struct{}{}, m.Controller.ExitAgentViewOnOwner(scope, m.options.Native.NowMillis())
		}
		return struct{}{}, m.Controller.EnterAgentViewOnOwner(scope, id, m.options.Native.NowMillis())
	})
	return err
}
