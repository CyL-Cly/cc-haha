package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// WorkingDirectory observes the actual per-session bootstrap after startup or
// resume. The desktop uses this authoritative cwd for launch metadata.
func (m *MainPrint) WorkingDirectory(ctx context.Context) (string, error) {
	if m.state == nil || m.closed {
		return "", cooperative.ErrClosed
	}
	var cwd string
	e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		cwd = m.state.Snapshot().CWD
		return nil
	})
	return cwd, e
}
