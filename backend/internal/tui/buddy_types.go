package tui

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"time"
)

type TerminalBuddySource interface {
	TerminalBuddySnapshot(context.Context) (json.RawMessage, error)
}

type terminalBuddyRuntime struct {
	value          protocol.JSONObject
	started        time.Time
	tick, petStart int
	petAt          json.RawMessage
	focused        bool
}
