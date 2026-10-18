package mcpruntime

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) BeginDiscoveryBatch(ctx context.Context) error {
	return s.options.AuthenticationCache.BeginBatch(ctx)
}

// ConnectForDiscovery is exclusively getMcpToolsCommandsAndResources's batch
// producer. Explicit reconnect and permission-time execution retain their real
// connectToServer probe, even when an earlier batch has cached a 401.
func (s *Service) ConnectForDiscovery(ctx context.Context, name string, config protocol.JSONObject, discover bool) (Connection, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return Connection{}, context.Canceled
	}
	enabled, err := s.options.Enabled(ctx, name, config)
	if err != nil {
		return Connection{}, err
	}
	if !enabled {
		return Connection{Name: name, Status: "disabled", Config: config.Clone(), Tools: []protocol.JSONObject{}, runtime: s}, nil
	}
	kind := config.String("type")
	if kind == "http" || kind == "sse" || kind == "claudeai-proxy" {
		cached, err := s.options.AuthenticationCache.Cached(ctx, name)
		if err != nil {
			return Connection{Name: name, Status: "failed", Config: config.Clone(), Tools: []protocol.JSONObject{}, Failure: err, runtime: s}, nil
		}
		if !cached && (kind == "http" || kind == "sse") && s.options.HasDiscoveryWithoutToken != nil {
			cached, err = s.options.HasDiscoveryWithoutToken(ctx, name, config)
			if err != nil {
				return Connection{}, err
			}
		}
		if cached {
			// This attests the actual cache producer without populating the separate
			// connectToServer memo. Copied Agent peers retain their graph identity;
			// CloseProbe cannot retire a later real connection under the same key.
			done := make(chan struct{})
			close(done)
			entry := &pending{done: done, cancel: func() {}}
			value := Connection{Name: name, Status: "needs-auth", Config: config.Clone(), Tools: []protocol.JSONObject{}, runtime: s, entry: entry}
			entry.value = value
			return clone(value), nil
		}
	}
	if discover {
		return s.Connect(ctx, name, config)
	}
	return s.ConnectTransport(ctx, name, config)
}
func (s *Service) clearAuthenticationFailuresInBackground() {
	if s.options.AuthenticationCache != nil {
		s.options.AuthenticationCache.queueClear(nil)
	}
}

// AuthenticationToolAuthorized matches the tool's un-awaited clear-all call;
// the following real reconnect will await its own targeted successful clear.
func (s *Service) AuthenticationToolAuthorized() { s.clearAuthenticationFailuresInBackground() }
