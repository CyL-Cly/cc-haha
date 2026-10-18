package clibootstrap

// Called only by the admitted MainCommand stack. Source's one-shot bootstrap
// slots belong to the process boundary, also borrowed by ordinary model work.
func (m *MainCommand) credentialFromDescriptor(key, filename string) string {
	if value, loaded := m.credentialCache[key]; loaded {
		return value
	}
	value := m.Native.credentialFromDescriptor(m.Native.Environment, m.credentialPlatform, m.credentialDirectory, key, filename)
	m.credentialCache[key] = value
	return value
}
