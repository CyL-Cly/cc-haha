package server

import "crypto/subtle"

// Both internal callers consume the one registered/revoked session authority.
// No raw credential is exposed outside its existing private process binding.
func (s *Server) authorizeSessionCredential(id, token string) bool {
	if id == "" || token == "" || s.collaboration == nil {
		return false
	}
	h := s.collaboration
	h.mu.Lock()
	current := h.tokens[id]
	h.mu.Unlock()
	return current != "" && subtle.ConstantTimeCompare([]byte(current), []byte(token)) == 1
}
