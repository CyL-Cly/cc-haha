package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeAgentMainLoopModelResolver uses the Server's actual auth/model
// producer. Its inputs are owned effective settings/environment snapshots;
// it must account for original subscription state and alias precedence.
type NativeAgentMainLoopModelResolver func(context.Context, protocol.JSONObject, map[string]string) (string, error)

func (p *NativeProcessSettings) SetAgentMainLoopModelResolver(resolver NativeAgentMainLoopModelResolver) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return mainCap("closed server Agent model producer")
	}
	p.agentModelResolver = resolver
	return nil
}
