package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) nativeStickersCommand(ctx context.Context, scope *cooperative.Scope) (string, error) {
	const address = "https://www.stickermule.com/claudecode"
	opened, err := m.openTerminalBrowserOnOwner(ctx, scope, address)
	if err != nil {
		return "", err
	}
	if opened {
		return "Opening sticker page in browser…", nil
	}
	return "Failed to open browser. Visit: " + address, nil
}
