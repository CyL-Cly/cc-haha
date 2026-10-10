package requestcontext

// NativeModelCapabilityOverride exposes Source's pinned model capability lookup
// without preparing a request. Presence is distinct from an explicit false.
func NativeModelCapabilityOverride(model string, environment map[string]string, capability string) (bool, bool) {
	return nativeCapability(Context{Options: Options{Model: model}, Host: Host{Env: environment}}, capability)
}
