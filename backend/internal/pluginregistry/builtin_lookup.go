package pluginregistry

// Lookup addresses the existing declaration registry without loading disk,
// materializing plugins, or evaluating availability. Source builtin toggles
// use registry membership even when a declaration is presently unavailable.
func (r *BuiltinRegistry) Lookup(name string) (BuiltinDefinition, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	definition, ok := r.definitions[name]
	if !ok {
		return BuiltinDefinition{}, false
	}
	if definition.Hooks != nil {
		copy := definition.Hooks.Clone()
		definition.Hooks = &copy
	}
	if definition.MCPServers != nil {
		copy := definition.MCPServers.Clone()
		definition.MCPServers = &copy
	}
	return definition, true
}
