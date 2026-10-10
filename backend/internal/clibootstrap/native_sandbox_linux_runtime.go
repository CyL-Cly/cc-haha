package clibootstrap

import (
	"path/filepath"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sandboxruntime"
)

type nativeLinuxSandboxPreparation struct {
	Status                                           sandboxruntime.LinuxDependencyStatus
	BPF, Apply, Socat, TemporaryDirectory, AssetRoot string
}

// Native Linux preparation runs in the existing process's physical producer.
// It neither creates an Owner nor initializes an Agent/ASK.
func (m *MainPrint) prepareNativeLinuxSandbox(scope *cooperative.Scope, install bool) (nativeLinuxSandboxPreparation, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) || !m.options.Native.NativeProcess {
		return nativeLinuxSandboxPreparation{}, cooperative.ErrRestricted
	}
	env := m.nativeOpenAIEnvironment()
	native := m.options.Native
	config := env["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(native.Home, ".claude")
	}
	// Bundled helper bytes are a process-owned asset, outside the default
	// writable command temp root. Source's vendor helper is likewise immutable
	// application material rather than a sandbox-created executable.
	assetRoot := filepath.Join(config, "Cache", "native-sandbox-runtime-0.0.44")
	temporary := env["TMPDIR"]
	if temporary == "" {
		temporary = env["TMP"]
	}
	if temporary == "" {
		temporary = env["TEMP"]
	}
	if temporary == "" {
		temporary = "/tmp"
	}
	future, e := nativeSkillWork(m, scope, func() (nativeLinuxSandboxPreparation, error) {
		result := nativeLinuxSandboxPreparation{TemporaryDirectory: temporary, AssetRoot: assetRoot}
		bwrap, e := nativeSandboxExecutable(env, "bwrap")
		result.Status.HasBwrap = e == nil && bwrap != ""
		socat, e := nativeSandboxExecutable(env, "socat")
		result.Status.HasSocat = e == nil && socat != ""
		result.Socat = socat
		if e := native.check("sandbox-native-asset", assetRoot); e != nil {
			return result, e
		}
		var bpf, apply string
		var installError error
		if install {
			bpf, apply, installError = sandboxruntime.InstallSeccompAssets(assetRoot, runtime.GOARCH)
		} else if runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64" {
			bpf = "bundled"
			apply = "bundled"
		}
		if installError == nil {
			result.BPF = bpf
			result.Apply = apply
			result.Status.HasSeccompBPF = bpf != ""
			result.Status.HasSeccompApply = apply != ""
		}
		// The original dependency check degrades missing seccomp to a warning.
		return result, nil
	})
	if e != nil {
		return nativeLinuxSandboxPreparation{}, e
	}
	return future.Await(scope)
}
