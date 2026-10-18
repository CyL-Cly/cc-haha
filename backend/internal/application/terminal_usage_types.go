package application

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type terminalUsagePanelState struct {
	value      protocol.JSONObject
	err        string
	loading    bool
	generation uint64
	job        *terminalUsageJob
}
type terminalUsageJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type terminalUsageRuntime struct {
	mu     sync.Mutex
	closed bool
	jobs   map[*terminalUsageJob]struct{}
}
