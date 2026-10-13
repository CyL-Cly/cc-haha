package ideintegration

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpheaders"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func NewNativeExtensions(home, cwd, platform, version string, env map[string]string, parentPID int) *Extensions {
	run := func(ctx context.Context, command string, args []string, environment map[string]string, timeout time.Duration) (ExtensionProcessResult, error) {
		active, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		out, e := worktree.NativeRun(active, worktree.ProcessRequest{Command: command, Arguments: args, CWD: cwd, Environment: environment})
		if active.Err() != nil {
			return ExtensionProcessResult{}, context.Cause(active)
		}
		return ExtensionProcessResult{Code: out.Code, Stdout: protocol.TrimJavaScriptString(out.Stdout), Stderr: protocol.TrimJavaScriptString(out.Stderr)}, e
	}
	parent := func(ctx context.Context) string {
		if platform != "macos" && platform != "darwin" {
			return ""
		}
		pid := parentPID
		for i := 0; i < 10 && pid > 1; i++ {
			out, e := run(ctx, "ps", []string{"-o", "command=", "-p", strconv.Itoa(pid)}, env, 10*time.Minute)
			if e != nil || out.Code != 0 {
				return ""
			}
			for _, entry := range []struct{ app, cli string }{{"Visual Studio Code.app", "code"}, {"Cursor.app", "cursor"}, {"Windsurf.app", "windsurf"}, {"Visual Studio Code - Insiders.app", "code"}, {"VSCodium.app", "codium"}} {
				if index := strings.Index(out.Stdout, entry.app+"/Contents/MacOS/Electron"); index >= 0 {
					return out.Stdout[:index+len(entry.app)] + "/Contents/Resources/app/bin/" + entry.cli
				}
			}
			out, e = run(ctx, "ps", []string{"-o", "ppid=", "-p", strconv.Itoa(pid)}, env, 10*time.Minute)
			if e != nil || out.Code != 0 || out.Stdout == "" {
				return ""
			}
			next := integerPrefix(out.Stdout)
			if next != next {
				return ""
			}
			pid = int(next)
		}
		return ""
	}
	return NewExtensions(ExtensionOptions{Home: home, Platform: platform, Version: version, Environment: env, Run: run, Stat: os.Stat, ReadDir: os.ReadDir, ParentCommand: parent, RunShell: func(ctx context.Context, command string) (ExtensionProcessResult, error) {
		out, e := mcpheaders.NativeRun(ctx, mcpheaders.Request{Command: command, CWD: cwd, Environment: env, Timeout: 10 * time.Minute})
		return ExtensionProcessResult{Code: out.Code, Stdout: out.Stdout}, e
	}})
}
