package toolsearch

import "encoding/json"

// Catalogue is captured by the real shared Host after permission/base/Agent
// filtering. Consumers must use this snapshot, not reconstruct a wider pool.
type Catalogue struct {
	Tools          []Tool
	PendingServers []json.RawMessage
	Gate           GateInputs
}
