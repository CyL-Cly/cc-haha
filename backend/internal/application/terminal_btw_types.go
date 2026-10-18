package application

import (
	"context"
	"sync"
)

type terminalBtwJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type terminalBtwRuntime struct {
	mu     sync.Mutex
	closed bool
	jobs   map[*terminalBtwJob]struct{}
}
type terminalBtwPanelState struct {
	question   string
	response   string
	err        string
	generation uint64
	job        *terminalBtwJob
}
