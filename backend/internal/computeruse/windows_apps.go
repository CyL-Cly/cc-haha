package computeruse

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Keep the compatibility helper's original JSON result at the HTTP boundary.
// Discovery does not install Python or rewrite an existing runtime.
func (s *Service) windowsInstalledApps(ctx context.Context) json.RawMessage {
	empty := json.RawMessage(`[]`)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return empty
	}
	s.operations.Add(1)
	s.mu.Unlock()
	defer s.operations.Done()
	python := s.windowsPythonPath()
	helper := filepath.Join(s.windowsRuntimeRoot(), "win_helper.py")
	if !exists(python) || !exists(helper) {
		return empty
	}
	result, err := s.run(ctx, python, []string{helper, "list_installed_apps"}, nil)
	if err != nil || result.Code != 0 {
		return empty
	}
	normalized, parseErr := protocol.ParseAndStringifyJavaScriptJSON([]byte(trim(result.Stdout)))
	if parseErr != nil {
		return empty
	}
	envelope, ok := decodeObject(normalized)
	if !ok || !truthyRaw(envelope["ok"]) {
		return empty
	}
	return envelope["result"]
}
