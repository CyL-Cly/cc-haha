package mcpruntime

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SDKManaged distinguishes setupSdkMcpClients from ordinary reconnect's failed
// SDK snapshot. The original projects their error and scope differently.
func (c Connection) SDKManaged() bool { return c.entry != nil && c.entry.sdk }

// PendingSDK retains the real SDK configuration's source before its handshake.
// Source publishes this client before sending the control response; no physical
// transport is opened until the ACK has entered StructuredIO's shared FIFO.
func (s *Service) PendingSDK(name string, config protocol.JSONObject) (Connection, error) {
	if config.String("type") != "sdk" || s.options.SDK == nil {
		return Connection{}, errors.New("pending SDK peer requires actual SDK binding")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Connection{}, context.Canceled
	}
	_, cancel := context.WithCancel(context.Background())
	entry := &pending{sdk: true, done: make(chan struct{}), cancel: cancel}
	value := Connection{Name: name, Status: "pending", Config: config.Clone(), Tools: []protocol.JSONObject{}, entry: entry, runtime: s}
	entry.value = value
	s.sdkAttempts[entry] = true
	close(entry.done)
	return clone(value), nil
}
