package clibootstrap

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// NativePolicyObservation is produced by the exact branch that selected the
// live policy value. A test-injected ResolvePolicy callback has no observation.
type NativePolicyObservation struct {
	Settings            *protocol.JSONObject
	Origin              string
	HasBase, HasDropIns bool
}

func (n *NativeBoundary) PolicyObservation() NativePolicyObservation {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.policyObservation == nil {
		return NativePolicyObservation{}
	}
	observation := *n.policyObservation
	if observation.Settings != nil {
		copy := observation.Settings.Clone()
		observation.Settings = &copy
	}
	return observation
}
