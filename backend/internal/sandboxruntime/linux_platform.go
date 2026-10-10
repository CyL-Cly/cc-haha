package sandboxruntime

import (
	"regexp"
	"strings"
)

var wslVersionMarker = regexp.MustCompile(`(?i)WSL([0-9]+)`)

// LinuxSupported implements the pinned platform primitive. WSL2+ follows
// Linux; WSL1 remains unsupported. Missing /proc/version is ordinary Linux.
func LinuxSupported(procVersion string) bool {
	if match := wslVersionMarker.FindStringSubmatch(procVersion); match != nil {
		return match[1] != "1"
	}
	return !strings.Contains(strings.ToLower(procVersion), "microsoft")
}

// CLI Linux and WSL labels are distinct from the runtime's Linux backend.
func LinuxCLIPlatform(procVersion string) string {
	value := strings.ToLower(procVersion)
	if strings.Contains(value, "microsoft") || strings.Contains(value, "wsl") {
		return "wsl"
	}
	return "linux"
}
