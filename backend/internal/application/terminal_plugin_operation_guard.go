package application

func terminalPluginOperationGuard(scope, operation string) string {
	if scope == "builtin" {
		if operation == "update" || operation == "uninstall" {
			return "Built-in plugins cannot be updated or uninstalled."
		}
		return ""
	}
	if operation != "install" && operation != "update" && scope != "" && scope != "user" && scope != "project" && scope != "local" {
		return "This plugin is managed by your organization. Contact your admin to disable it."
	}
	return ""
}
