package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"io"
)

func (m *MainPrint) nativeSessionEndOnOwner(scope *cooperative.Scope, reason string) error {
	if !m.options.Native.NativeProcess || m.agentChild || m.hook == nil || m.sessionEndRan {
		return nil
	}
	m.sessionEndRan = true
	budget := queryhooks.SessionEndTimeout(m.options.Native.Environment["CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS"])
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	// gracefulShutdown catches Hook exceptions after persistence cleanup. Actual
	// process/transport refs still finish before the shared owner is closed.
	_ = m.hook.ExecuteSessionEnd(ctx, reason, queryhooks.Context{Scope: scope}, budget, func(line json.RawMessage) error {
		var text string
		if err := json.Unmarshal(line, &text); err != nil {
			return err
		}
		if m.options.Error == nil {
			return nil
		}
		_, err := io.WriteString(m.options.Error, text)
		return err
	})
	return nil
}
