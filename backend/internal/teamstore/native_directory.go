package teamstore

func (c *Config) configDirectory() string {
	if directory, bound := c.options.TaskStore.NativeConfigDirectory(); bound {
		return directory
	}
	return c.options.ConfigDir
}

func (m *Mailbox) configDirectory() string {
	if directory, bound := m.options.TaskStore.NativeConfigDirectory(); bound {
		return directory
	}
	return m.options.ConfigDir
}
