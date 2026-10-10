package sandboxruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

type LinuxWrapOptions struct {
	Filesystem                                                              *LinuxFilesystem
	Read                                                                    *ReadRestriction
	Write                                                                   *WriteRestriction
	NeedsNetworkRestriction, EnableWeakerNestedSandbox, AllowAllUnixSockets bool
	HTTPSocketPath, SOCKSSocketPath                                         string
	HTTPProxyPort, SOCKSProxyPort                                           *float64
	Shell                                                                   string
	Environment                                                             map[string]string
	FindExecutable                                                          func(string) (string, error)
	SeccompBPFPath, ApplySeccompPath                                        string
}

func shellArguments(args []string) string {
	result := make([]string, len(args))
	for i, arg := range args {
		result[i] = teamspawn.ShellQuote(arg)
	}
	return strings.Join(result, " ")
}
func buildLinuxSandboxCommand(httpSocket, socksSocket, command, filter, shell, apply string) (string, error) {
	setup := []string{
		"socat TCP-LISTEN:3128,fork,reuseaddr UNIX-CONNECT:" + httpSocket + " >/dev/null 2>&1 &",
		"socat TCP-LISTEN:1080,fork,reuseaddr UNIX-CONNECT:" + socksSocket + " >/dev/null 2>&1 &",
		`trap "kill %1 %2 2>/dev/null; exit" EXIT`,
	}
	if filter != "" {
		if apply == "" {
			return "", errors.New("apply-seccomp binary not found. This should have been caught earlier. Ensure vendor/seccomp/{x64,arm64}/apply-seccomp binaries are included in the package.")
		}
		setup = append(setup, shellArguments([]string{apply, filter, shell, "-c", command}))
	} else {
		setup = append(setup, "eval "+teamspawn.ShellQuote(command))
	}
	return shell + " -c " + teamspawn.ShellQuote(strings.Join(setup, "\n")), nil
}

// WrapLinux invokes native observations and emits the original bwrap/inner
// socat command. The caller owns the bridge, seccomp resources and execution;
// this producer cannot itself authorize or spawn an application command.
func WrapLinux(ctx context.Context, command string, o LinuxWrapOptions) (string, error) {
	if !o.NeedsNetworkRestriction && (o.Read == nil || len(o.Read.DenyOnly) == 0) && o.Write == nil {
		return command, nil
	}
	args := []string{"--new-session", "--die-with-parent"}
	filter := ""
	apply := ""
	if !o.AllowAllUnixSockets && exists(o.SeccompBPFPath) && exists(o.ApplySeccompPath) {
		filter = o.SeccompBPFPath
		apply = o.ApplySeccompPath
	}
	if o.NeedsNetworkRestriction {
		args = append(args, "--unshare-net")
		if o.HTTPSocketPath != "" && o.SOCKSSocketPath != "" {
			if !exists(o.HTTPSocketPath) {
				return "", fmt.Errorf("Linux HTTP bridge socket does not exist: %s. The bridge process may have died. Try reinitializing the sandbox.", o.HTTPSocketPath)
			}
			if !exists(o.SOCKSSocketPath) {
				return "", fmt.Errorf("Linux SOCKS bridge socket does not exist: %s. The bridge process may have died. Try reinitializing the sandbox.", o.SOCKSSocketPath)
			}
			args = append(args, "--bind", o.HTTPSocketPath, o.HTTPSocketPath, "--bind", o.SOCKSSocketPath, o.SOCKSSocketPath)
			http, socks := float64(3128), float64(1080)
			for _, value := range ProxyEnvironment(o.Environment, "linux", &http, &socks) {
				i := strings.IndexByte(value, '=')
				args = append(args, "--setenv", value[:i], value[i+1:])
			}
			if o.HTTPProxyPort != nil {
				args = append(args, "--setenv", "CLAUDE_CODE_HOST_HTTP_PROXY_PORT", numberString(*o.HTTPProxyPort))
			}
			if o.SOCKSProxyPort != nil {
				args = append(args, "--setenv", "CLAUDE_CODE_HOST_SOCKS_PROXY_PORT", numberString(*o.SOCKSProxyPort))
			}
		}
	}
	if o.Filesystem == nil || o.FindExecutable == nil {
		return "", errors.New("Linux wrapper requires owning native filesystem and executable lookup")
	}
	fs, e := o.Filesystem.Arguments(ctx, o.Read, o.Write)
	if e != nil {
		return "", e
	}
	args = append(args, fs...)
	args = append(args, "--dev", "/dev", "--unshare-pid")
	if !o.EnableWeakerNestedSandbox {
		args = append(args, "--proc", "/proc")
	}
	name := o.Shell
	if name == "" {
		name = "bash"
	}
	shell, e := o.FindExecutable(name)
	if e != nil || shell == "" {
		return "", fmt.Errorf("Shell '%s' not found in PATH", name)
	}
	args = append(args, "--", shell, "-c")
	if o.NeedsNetworkRestriction && o.HTTPSocketPath != "" && o.SOCKSSocketPath != "" {
		inner, e := buildLinuxSandboxCommand(o.HTTPSocketPath, o.SOCKSSocketPath, command, filter, shell, apply)
		if e != nil {
			return "", e
		}
		args = append(args, inner)
	} else if filter != "" {
		args = append(args, shellArguments([]string{apply, filter, shell, "-c", command}))
	} else {
		args = append(args, command)
	}
	return shellArguments(append([]string{"bwrap"}, args...)), nil
}
