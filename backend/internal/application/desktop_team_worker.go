package application

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (d *DesktopSession) AcknowledgeTeamWorkerModel(ctx context.Context, model string) (protocol.JSONObject, error) {
	d.runtimeMu.Lock()
	defer d.runtimeMu.Unlock()
	return d.currentEngine().process.AcknowledgeTeamWorkerModel(ctx, model)
}

func (d *DesktopSession) SubmitTeamTask(ctx context.Context, input protocol.JSONObject) error {
	d.admission.Lock()
	defer d.admission.Unlock()
	d.runtimeMu.Lock()
	d.mu.Lock()
	if d.closed || d.options.TeamWorker == nil {
		d.mu.Unlock()
		d.runtimeMu.Unlock()
		return errors.New("CLI session is not running")
	}
	d.running, d.foregroundPending = true, true
	d.projection.Stopped = false
	engine := d.engine
	d.mu.Unlock()
	d.runtimeMu.Unlock()
	d.emit(frame("status", field("state", "thinking"), field("verb", "Thinking")))
	err := engine.submitSDKEvents(ctx, input, d.events)
	d.mu.Lock()
	d.running, d.foregroundPending = false, false
	d.mu.Unlock()
	return err
}
