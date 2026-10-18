package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The original timeout getters read process.env at each invocation. Read the
// actual shared mutable environment on its Owner; transport subprocess env
// remains an immutable snapshot and no ambient machine variables are read.
func (m *MainPrint) nativeMCPEnvironmentValue(ctx context.Context, name string) (string, error) {
	var value string
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error {
		current := m.environment.Get(name)
		if current == nil {
			return nil
		}
		text, err := protocol.ExactJavaScriptStringJSON(current)
		if err != nil {
			return err
		}
		return json.Unmarshal(text, &value)
	})
	if err != nil {
		return "", err
	}
	if err := receipt.Wait(); err != nil {
		return "", err
	}
	return value, nil
}
