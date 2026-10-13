package server

import "context"

// A desktop worker's actual Host can finish its graceful shutdown without a
// public Stop request. Source then receives the child process's exit event and
// removes its conversation registry entry. The Go factory-owned identity is
// that exit event's counterpart; an old callback may never retire a replacement.
func (s *Server) retireSelfClosedRuntime(ctx context.Context, id string, expected *runtimeSession, ended LiveSession) error {
	if ended == nil {
		return nil
	}
	s.mu.Lock()
	if s.runtimes[id] != expected {
		s.mu.Unlock()
		return nil
	}
	expected.mu.Lock()
	if expected.live != ended || expected.retired {
		expected.mu.Unlock()
		s.mu.Unlock()
		return nil
	}
	// Claim this exact live generation before releasing either lock. Ordinary
	// admission/restart sees retirement immediately; Close below still joins
	// actual native/SDK work and revokes this generation's credentials.
	expected.retired = true
	expected.mu.Unlock()
	s.mu.Unlock()
	if err := closeRuntime(ctx, expected); err != nil {
		return err
	}
	s.mu.Lock()
	if s.runtimes[id] == expected {
		delete(s.runtimes, id)
	}
	s.mu.Unlock()
	return nil
}
