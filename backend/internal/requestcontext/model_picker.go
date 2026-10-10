package requestcontext

// NativeModelPickerEffort exposes the existing direct relay effort policy to
// the terminal picker. It takes original model/environment inputs; it does not
// prepare an inference request or run a second provider consumer.
func NativeModelPickerEffort(model string, environment map[string]string) (supported, max bool, defaultLevel string) {
	input := Context{Options: Options{Model: model}, Host: Host{Profile: NativeDirectRelayProfile, Env: environment}}
	supported = nativeSupportsEffort(input)
	max = nativeSupportsEffortLevel(input, "max")
	defaults := input
	defaults.Host.Env = map[string]string{}
	defaultLevel = stringValue(nativeEffort(defaults))
	if defaultLevel == "" {
		defaultLevel = "high"
	}
	return
}
