package taskstore

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// List is Source listTasks: the direct physical read without causal snapshot
// or deleted-incarnation terminal fallback. Tool callers keep Snapshot.
func (s *Service) List(list string) ([]protocol.JSONObject, error) { return s.list(list) }
