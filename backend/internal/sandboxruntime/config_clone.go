package sandboxruntime

func cloneSlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	return append([]T{}, value...)
}
func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneConfig(value Config) Config {
	value.Network = cloneNetworkConfig(value.Network)
	value.Filesystem.DenyRead = cloneSlice(value.Filesystem.DenyRead)
	value.Filesystem.AllowRead = cloneSlice(value.Filesystem.AllowRead)
	value.Filesystem.AllowWrite = cloneSlice(value.Filesystem.AllowWrite)
	value.Filesystem.DenyWrite = cloneSlice(value.Filesystem.DenyWrite)
	value.IgnoreViolations = cloneSlice(value.IgnoreViolations)
	value.EnableWeakerNestedSandbox = clonePointer(value.EnableWeakerNestedSandbox)
	value.EnableWeakerNetworkIsolation = clonePointer(value.EnableWeakerNetworkIsolation)
	value.Ripgrep = value.Ripgrep.Clone()
	return value
}
