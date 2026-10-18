package application

import (
	"context"
	"encoding/json"
)

func (s *Session) FetchTerminalPRStatus(ctx context.Context) (json.RawMessage, error) {
	return s.process.FetchTerminalPRStatus(ctx)
}

func terminalPRSettingEnabled(value json.RawMessage) bool {
	if len(value) == 0 || string(value) == "null" {
		return true
	}
	return sourceSDKTruthy(value)
}
