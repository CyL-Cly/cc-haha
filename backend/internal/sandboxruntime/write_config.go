package sandboxruntime

// FilesystemWriteRestriction is the original sandbox-runtime getFsWriteConfig
// contract. Linux excludes remaining glob patterns after stripping /**; neither
// this getter nor the permission consumer expands configured glob patterns.
func FilesystemWriteRestriction(config FilesystemConfig, platform, home string) WriteRestriction {
	result := WriteRestriction{AllowOnly: DefaultWritePaths(home), DenyWithinAllow: []string{}}
	for _, row := range []struct {
		paths []string
		out   *[]string
	}{{config.AllowWrite, &result.AllowOnly}, {config.DenyWrite, &result.DenyWithinAllow}} {
		for _, value := range row.paths {
			value = StripGlobSuffix(value)
			if platform != "linux" || !ContainsGlob(value) {
				*row.out = append(*row.out, value)
			}
		}
	}
	return result
}

// WriteConfig returns an immutable snapshot under the same lock as updates.
func (m *Manager) WriteConfig() WriteRestriction {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return FilesystemWriteRestriction(m.config.Filesystem, m.options.Platform, m.options.Home)
}
