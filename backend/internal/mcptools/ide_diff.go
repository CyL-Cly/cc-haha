package mcptools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ConnectedIDE projects the actual connected peer for the terminal's direct
// IDE RPC consumer. This is independent of tools/list advertisement.
func (s *Service) ConnectedIDE(scope *cooperative.Scope) (*mcpclient.Client, protocol.JSONObject, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, protocol.JSONObject{}, cooperative.ErrRestricted
	}
	for _, peer := range s.connections {
		if peer.Name == "ide" && peer.Status == "connected" && peer.Client != nil {
			return peer.Client, peer.Config.Clone(), nil
		}
	}
	return nil, protocol.JSONObject{}, nil
}
