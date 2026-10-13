package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// TerminalPermissionRuleOptions reads the same managed setting as Source
// shouldShowAlwaysAllowOptions. It does not mint a grant or alter model tools.
func (m *MainPrint) TerminalPermissionRuleOptions(scope *cooperative.Scope, toolName, command string) (bool, []string, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) || m.closed {
		return false, nil, cooperative.ErrRestricted
	}
	settings, err := m.Startup.GetSettingsForSource("policySettings")
	if err != nil {
		return false, nil, err
	}
	if settings != nil {
		if string(settings.Get("allowManagedPermissionRulesOnly")) == "true" {
			return false, nil, nil
		}
	}
	if toolName != "PowerShell" {
		return true, nil, nil
	}
	if m.nativeTools == nil {
		return false, nil, mainCap("actual PowerShell terminal prefix service")
	}
	prefixes, err := m.nativeTools.PowerShellApprovalPrefixes(scope, command)
	return true, prefixes, err
}
