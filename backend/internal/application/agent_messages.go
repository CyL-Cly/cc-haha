package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Agent messages belong to the existing parent task/coordinator, not its main inbox.
func (d *DesktopSession) SendAgentMessage(ctx context.Context, id, content string) (protocol.JSONObject, error) {
	d.runtimeMu.Lock()
	defer d.runtimeMu.Unlock()
	d.mu.Lock()
	closed, engine := d.closed, d.engine
	d.mu.Unlock()
	if closed {
		return protocol.JSONObject{}, context.Canceled
	}
	return engine.process.SendAgentMessage(ctx, id, content)
}
