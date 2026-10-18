package powershellpolicy

import (
	"os"
	"os/exec"
	"testing"
)

// Keep physical AST and shell consumers available with an explicitly prepared
// private interpreter; a missing explicitly selected executable must fail.
func physicalPowerShell(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CC_HAHA_TEST_PWSH"); path != "" {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("explicit physical PowerShell fixture is unavailable: %s (%v)", path, err)
		}
		return path
	}
	if path, err := exec.LookPath("pwsh"); err == nil {
		return path
	}
	const fallback = "/opt/homebrew/bin/pwsh"
	if info, err := os.Stat(fallback); err == nil && info.Mode().IsRegular() {
		return fallback
	}
	t.Skip("requires a real physical PowerShell interpreter")
	return ""
}
