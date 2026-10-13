package remoteio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) takeHybridStream() []protocol.JSONObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streamTimer != nil {
		s.streamTimer.Stop()
		s.streamTimer = nil
	}
	buffer := s.streamBuffer
	s.streamBuffer = nil
	return buffer
}
func (s *Service) flushHybridStream() { _ = s.hybrid.enqueue(s.ctx, s.takeHybridStream()) }
func (s *Service) writeHybrid(message protocol.JSONObject) error {
	if message.String("type") == "stream_event" {
		s.mu.Lock()
		if !s.closed {
			s.streamBuffer = append(s.streamBuffer, message)
			if s.streamTimer == nil {
				s.streamTimer = time.AfterFunc(100*time.Millisecond, func() { s.spawn(s.flushHybridStream) })
			}
		}
		s.mu.Unlock()
		return nil
	}
	buffer := append(s.takeHybridStream(), message)
	if err := s.hybrid.enqueue(s.ctx, buffer); err != nil {
		return err
	}
	return s.hybrid.flush(s.ctx)
}
func (s *Service) postHybrid(ctx context.Context, events []protocol.JSONObject) error {
	token := s.ingressToken()
	if token == "" {
		s.diagnostic("cli_hybrid_post_no_token")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoints.Events, bytes.NewReader(raw(map[string]any{"events": events})))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.doGlobalHTTP(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != 429 {
		s.diagnostic("cli_hybrid_post_client_error")
		return nil
	}
	return fmt.Errorf("POST failed with %d", response.StatusCode)
}
