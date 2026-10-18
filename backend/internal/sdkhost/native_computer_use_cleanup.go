package sdkhost

// BeginComputerUseCleanupClose is administrative cancellation, before joining
// an ASK that may already be waiting on its independent native CU cleanup.
func (h *Host) BeginComputerUseCleanupClose() {
	if h != nil && h.nativeComputerUseCleanup != nil {
		h.nativeComputerUseCleanup.BeginClose()
	}
}
