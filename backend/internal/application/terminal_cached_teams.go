package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Session) SubscribeTerminalCachedTeams(ctx context.Context, listener func([]protocol.JSONObject)) ([]protocol.JSONObject, func(), error) {
	return s.process.SubscribeTerminalCachedTeams(ctx, listener)
}
