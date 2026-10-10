package application

import (
	"context"
	"encoding/json"
)

func (d *DesktopSession) SetTeamRuntimeSnapshot(ctx context.Context, name string, createdAt json.RawMessage) error {
	d.runtimeMu.Lock()
	defer d.runtimeMu.Unlock()
	return d.currentEngine().process.SetTeamRuntimeSnapshot(ctx, name, createdAt)
}
func (d *DesktopSession) SessionPermissionMode(ctx context.Context) (string, error) {
	d.runtimeMu.Lock()
	defer d.runtimeMu.Unlock()
	return d.currentEngine().process.SessionPermissionMode(ctx)
}
