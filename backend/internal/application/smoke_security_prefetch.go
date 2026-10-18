package application

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

// SmokeSecurityPrefetch binds only the private smoke profile's external
// execFile primitive. Each read has a real started child and an absent result;
// the normal Store still owns waiting, parsing and cache publication.
func SmokeSecurityPrefetch(environment map[string]string) (func(context.Context, securestorage.Command) (*securestorage.KeychainPrefetchProcess, error), error) {
	security, err := SmokeSecurity(environment)
	if err != nil || security == nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(environment["CC_HAHA_ELECTRON_SMOKE_STATE_ROOT"])
	if err != nil {
		return nil, err
	}
	home := environment["HOME"]
	if runtime.GOOS == "windows" {
		home = environment["USERPROFILE"]
	}
	// Do not inherit shell startup hooks, provider tokens or a PATH-selected
	// security executable. Every user-state path exposed to this child is private.
	childEnvironment := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + root, "TMP=" + root, "TEMP=" + root}
	systemRoot := environment["SystemRoot"]
	if systemRoot == "" {
		systemRoot = environment["SYSTEMROOT"]
	}
	return func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
		if ctx == nil || len(call.Args) != 6 || call.Args[0] != "find-generic-password" || call.Args[1] != "-a" || call.Args[2] == "" || call.Args[3] != "-w" || call.Args[4] != "-s" || call.Args[5] == "" || call.Input != "" || call.ShellScript != "" {
			return nil, errors.New("desktop smoke prefetch requires the actual read-only execFile operation")
		}
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		executable := "/bin/sh"
		arguments := []string{"-c", "exit 44"}
		launchEnvironment := append([]string{}, childEnvironment...)
		if runtime.GOOS == "windows" {
			// Windows commands do not use the macOS prefetch producer. Validate
			// this runtime only if an actual subprocess launch is requested.
			if !filepath.IsAbs(systemRoot) {
				return nil, errors.New("desktop smoke prefetch requires an absolute Windows system runtime")
			}
			executable = filepath.Join(systemRoot, "System32", "cmd.exe")
			arguments = []string{"/d", "/s", "/c", "exit 44"}
			launchEnvironment = append(launchEnvironment, "SystemRoot="+systemRoot)
		}
		command := exec.CommandContext(ctx, executable, arguments...)
		command.Dir, command.Env = root, launchEnvironment
		return securestorage.StartKeychainPrefetchProcess(ctx, command)
	}, nil
}
