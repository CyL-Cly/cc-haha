package clibootstrap

import (
	"context"
	"encoding/json"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// CleanupInstallationLegacy is called by the explicit install command after
// native installation. Update and startup diagnostics never remove npm/aliases.
func (m *MainCommand) CleanupInstallationLegacy(ctx context.Context, scope *cooperative.Scope) (cliinstallation.LegacyCleanup, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || !m.Native.NativeProcess {
		return cliinstallation.LegacyCleanup{}, cooperative.ErrRestricted
	}
	packageName, specified := m.Native.Environment["CLAUDE_CODE_LOCAL_PACKAGE_URL"]
	if !specified {
		packageName = "claude-code-local"
	}
	installer, err := cliinstallation.New(cliinstallation.Options{Home: m.Native.Home, CWD: m.Native.CWD, Environment: m.Native.Environment, Platform: m.credentialPlatform, Architecture: runtime.GOARCH, PackageName: packageName})
	if err != nil {
		return cliinstallation.LegacyCleanup{}, err
	}
	payload, err := scope.AwaitBytes(cooperative.TranscriptAppend, func(worker context.Context) ([]byte, error) {
		result := installer.CleanupNPM(worker)
		result.Aliases = installer.CleanupShellAliases()
		return json.Marshal(result)
	})
	if err != nil {
		return cliinstallation.LegacyCleanup{}, err
	}
	var result cliinstallation.LegacyCleanup
	err = json.Unmarshal(payload, &result)
	return result, err
}
