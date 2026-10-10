package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
)

// earlySettingsFailure is a finite normal-entry boundary, not a successful
// command dispatcher. The observed Source profile is external, bare, no-color
// and pipe output. Native MDM/keychain, import telemetry, help, init and every
// successful handler remain outside this boundary and retain the main guard.
// Source eager settings writes precede a later setting-source failure.
func earlySettingsFailure(args []string) (int, bool) {
	if !earlySettingsPlatform(runtime.GOOS, runtime.GOARCH) || !earlySettingsArgs(args) || os.Getenv("USER_TYPE") != "external" || os.Getenv("NO_COLOR") != "1" {
		return 0, false
	}
	// Active debug, remote bootstrap, inspection and a terminal output profile
	// have startup effects that this finite boundary has not migrated.
	for _, key := range []string{"DEBUG", "DEBUG_SDK", "CLAUDE_CODE_REMOTE", "NODE_OPTIONS", "FORCE_COLOR", "CLAUDE_CODE_ACTION", "CLAUDE_CODE_PROFILE_STARTUP"} {
		if os.Getenv(key) != "" {
			return 0, false
		}
	}
	info, err := os.Stdout.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return 0, false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return 0, false
	}
	// These are real Source bootstrap assignments, before eagerLoadSettings.
	_ = os.Setenv("COREPACK_ENABLE_AUTO_PIN", "0")
	_ = os.Setenv("CLAUDE_CODE_SIMPLE", "1")
	_ = os.Setenv("NoDefaultCurrentDirectoryInExePath", "1")
	if os.Getenv("CLAUDE_CODE_ENTRYPOINT") == "" {
		_ = os.Setenv("CLAUDE_CODE_ENTRYPOINT", "sdk-cli")
	}
	result := clibootstrap.Prepare(args, clibootstrap.Options{WorkingDir: cwd, TempDir: os.TempDir()})
	if result.ExitCode == 0 {
		return 0, false
	}
	fmt.Fprint(os.Stderr, result.Stderr)
	return result.ExitCode, true
}

// Admit only the actual eager-settings argument family. Root version/Recovery
// stay separate; unknown flags/operands and successful handlers stay guarded.
func earlySettingsArgs(args []string) bool {
	bare, settings := false, false
	for n := 0; n < len(args); n++ {
		arg := args[n]
		switch {
		case arg == "--bare":
			bare = true
		case arg == "--settings" || arg == "--setting-sources":
			settings = true
			if n+1 == len(args) {
				return false
			}
			n++
		case strings.HasPrefix(arg, "--settings=") || strings.HasPrefix(arg, "--setting-sources="):
			settings = true
		default:
			return false
		}
	}
	return bare && settings
}

// earlySettingsPlatform declares the finite native runtime domain. Calling this
// predicate with another target proves rejection only; it is not a cross-platform
// execution or evidence that Source startup effects have been migrated there.
func earlySettingsPlatform(goos, arch string) bool {
	return goos == "darwin" && arch == "arm64"
}
