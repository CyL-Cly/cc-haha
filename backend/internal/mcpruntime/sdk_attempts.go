package mcpruntime

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ConnectSDKAttempt mirrors setupSdkMcpClients, which creates a new Client for
// every server on each changed-name/failed-client pass. These attempts remain in
// the same process namespace: a cached Tool closure or background child can
// still own an earlier peer. Normal MCP connection memoization is unchanged.
func (s *Service) ConnectSDKAttempt(ctx context.Context, name string, config protocol.JSONObject) (Connection, error) {
	if config.String("type") != "sdk" || s.options.SDK == nil {
		return Connection{}, errors.New("SDK attempt requires actual SDK control binding")
	}
	enabled, err := s.options.Enabled(ctx, name, config)
	if err != nil {
		return Connection{}, err
	}
	if !enabled {
		return Connection{Name: name, Status: "disabled", Config: config.Clone(), Tools: []protocol.JSONObject{}, runtime: s}, nil
	}
	key := s.key(name, config)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Connection{}, context.Canceled
	}
	lifetime, cancel := context.WithCancel(context.Background())
	entry := &pending{sdk: true, done: make(chan struct{}), cancel: cancel}
	s.sdkAttempts[entry] = true
	s.sdkCurrent[key] = entry
	go s.connect(lifetime, key, name, config.Clone(), entry)
	s.mu.Unlock()
	select {
	case <-entry.done:
	case <-ctx.Done():
		return Connection{}, context.Cause(ctx)
	}
	if entry.err != nil {
		return Connection{}, entry.err
	}
	value := clone(entry.value)
	enabled, err = s.options.Enabled(ctx, name, config)
	if err != nil {
		return Connection{}, err
	}
	if !enabled {
		if err = s.closeSDKAttempt(context.Background(), value); err != nil {
			return Connection{}, err
		}
		value.Status, value.Client, value.Tools = "disabled", nil, []protocol.JSONObject{}
		return value, nil
	}
	// Source does not call its memoized fetchToolsForClient without tools capability.
	if value.Status == "connected" && value.Client.Capabilities().Get("tools") != nil {
		return s.discover(ctx, key, value)
	}
	return value, nil
}

// ExecutionConnection is the immutable original peer captured by Source's
// memoized Tool closure. Status and notifications continue to use the current
// Client, while execution uses this peer after shared-graph ownership checking.
func (c Connection) ExecutionConnection() Connection {
	if c.execution != nil {
		return clone(*c.execution)
	}
	return clone(c)
}

func (s *Service) closeSDKAttempt(ctx context.Context, connection Connection) error {
	if !s.OwnsConnection(connection) {
		return errors.New("SDK cleanup requires its actual owned attempt")
	}
	entry := connection.entry
	s.mu.Lock()
	if entry == nil || !entry.sdk || connection.runtime != s {
		s.mu.Unlock()
		return errors.New("SDK cleanup requires its actual owned attempt")
	}
	key := s.key(connection.Name, connection.Config)
	if s.sdkCurrent[key] == entry {
		delete(s.sdkCurrent, key)
	}
	s.mu.Unlock()
	entry.cancel()
	select {
	case <-entry.done:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if entry.value.Client != nil {
		return entry.value.Client.Close()
	}
	return nil
}
