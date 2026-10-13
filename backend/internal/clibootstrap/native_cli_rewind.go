package clibootstrap

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// RewindCLIFileCheckpoint consumes the active original user-message UUID and
// checkpoint service without submitting a turn or changing the conversation.
func (m *MainPrint) RewindCLIFileCheckpoint(ctx context.Context, id string) error {
	return m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.initializeOnOwner(scope); err != nil {
			return err
		}
		history, err := m.Host.OriginalHistoryOnOwner(scope)
		if err != nil {
			return err
		}
		user := false
		for _, message := range history {
			if message.Record.String("uuid") == id && message.Record.String("type") == "user" {
				user = true
				break
			}
		}
		if !user {
			return fmt.Errorf("Error: --rewind-files requires a user message UUID, but %s is not a user message in this session", id)
		}
		if m.fileHistory == nil {
			return fmt.Errorf("Error: File rewinding is not enabled.")
		}
		enabled, err := m.fileHistory.Enabled(scope)
		if err != nil {
			return err
		}
		if !enabled {
			return fmt.Errorf("Error: File rewinding is not enabled.")
		}
		preview, err := m.fileHistory.PreviewRestore(scope, id)
		if err != nil {
			return err
		}
		if !preview.Available {
			return fmt.Errorf("Error: No file checkpoint found for this message.")
		}
		if err = m.fileHistory.Rewind(ctx, scope, id); err != nil {
			return fmt.Errorf("Error: Failed to rewind: %s", err.Error())
		}
		return nil
	})
}
