package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) nativeContextCommand(ctx context.Context, scope *cooperative.Scope) (string, error) {
	value, err := m.nativeContextUsageWithContext(ctx, scope, false)
	if err != nil {
		return "", err
	}
	encoded, err := value.MarshalJSON()
	if err != nil {
		return "", err
	}
	var data contextusage.Data
	if err = json.Unmarshal(encoded, &data); err != nil {
		return "", err
	}
	return contextusage.Markdown(data, m.options.Native.Environment["USER_TYPE"] == "ant"), nil
}
