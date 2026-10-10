package sdkhost

// The keyword setting is accepted only with both original input/round
// attachments and the owning Workflow authorization state callback.
func nativeWorkflowKeywordBound(options Options) bool {
	return options.Print != nil && options.Print.NativeProcess && options.Print.InputAttachments != nil && options.Print.PlanAttachments != nil && options.Print.Tools != nil && options.Print.Tools.HasWorkflowBindings()
}
