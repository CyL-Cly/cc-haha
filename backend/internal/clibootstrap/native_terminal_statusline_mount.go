package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) MountTerminalStatusLine(ctx context.Context) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		projection, err := m.nativeStatusLineProjection(scope)
		if err != nil || !projection.State.Configured {
			return err
		}
		hook, _ := projection.Settings.Object("statusLine")
		command, _ := protocol.DecodeJavaScriptJSONString(hook.Get("command"))
		metadata := protocol.NewJSONObject(fieldMain("command_length", len(command)))
		if padding := hook.Get("padding"); padding != nil {
			metadata.Set("padding", padding)
		}
		if err := m.reportNativeFileEvent(scope, analytics.Event{Name: "tengu_status_line_mount", Metadata: metadata}); err != nil {
			return err
		}
		if projection.State.UserHooksDisabled {
			if err := m.nativeStatusLineDebugOnOwner(scope, "Status line is configured but disableAllHooks is true", "WARN"); err != nil {
				return err
			}
		}
		if !projection.State.Trusted {
			return m.nativeStatusLineDebugOnOwner(scope, "Status line command skipped: workspace trust not accepted", "WARN")
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
