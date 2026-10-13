package clibootstrap

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkbetas"
)

func (m *MainPrint) initializeNativeCLIBetas() error {
	if len(m.options.Arguments.Betas) == 0 {
		return nil
	}
	return m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		subscriber, err := m.openAIAuth.IsClaudeSubscriber(context.Background(), scope, !m.options.Interactive)
		if err != nil {
			return err
		}
		values, warnings := sdkbetas.Filter(m.options.Arguments.Betas, subscriber)
		m.nativeSDKBetas = values
		if m.options.Error != nil {
			for _, warning := range warnings {
				if _, err := fmt.Fprintln(m.options.Error, warning); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
