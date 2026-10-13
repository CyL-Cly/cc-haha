package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RegisterCLIConfigurations installs the original parsed SDK configurations,
// including config.name and dynamic provenance, before stdin initialize.
func (s *NativeSDKMCPState) RegisterCLIConfigurations(scope *cooperative.Scope, configs protocol.JSONObject) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	s.configurations.Assign(configs)
	return nil
}
