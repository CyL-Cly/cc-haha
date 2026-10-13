package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Initial CLI configs exist before the SDK stdin transport binds. Discovery
// must not permanently memoize a failed client from that unbound transport.
func (s *NativeSDKMCPState) ConfigurationsForDiscovery(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	if !s.open {
		return protocol.NewJSONObject(), nil
	}
	return s.configurations.Clone(), nil
}
