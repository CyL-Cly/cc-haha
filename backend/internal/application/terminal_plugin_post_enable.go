package application

func terminalPluginPostEnableMessage(name, outcome, detail string) string {
	switch outcome {
	case "configured":
		return "✓ Enabled and configured " + name + ". Run /reload-plugins to apply."
	case "skipped":
		return "✓ Enabled " + name + ". Run /reload-plugins to apply."
	case "error":
		return "Failed to save configuration: " + detail
	}
	return ""
}
