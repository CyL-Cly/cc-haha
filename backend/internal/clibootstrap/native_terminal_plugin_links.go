package clibootstrap

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// Plugin links reuse the Source browser boundary: validate HTTP(S), use the
// explicit BROWSER/OS opener, and leave the detail view in place on failure.
func (m *MainPrint) OpenTerminalPluginLink(ctx context.Context, address string) (bool, error) {
	opened := false
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var err error
		opened, err = m.openTerminalBrowserOnOwner(ctx, scope, address)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return opened, err
}

func (m *MainPrint) openTerminalBrowserOnOwner(ctx context.Context, scope *cooperative.Scope, address string) (bool, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil {
		return false, cooperative.ErrRestricted
	}
	parsed, err := weburl.Parse(address)
	if err != nil || parsed.Scheme() != "http" && parsed.Scheme() != "https" {
		return false, nil
	}
	if m.closed {
		return false, cooperative.ErrClosed
	}
	native, platform := m.options.Native, m.options.Platform
	command := native.Environment["BROWSER"]
	args := []string{address}
	if platform == "windows" || platform == "win32" {
		if command == "" {
			command, args = "rundll32", []string{"url,OpenURL", address}
		} else {
			args = []string{`"` + address + `"`}
		}
	} else if command == "" {
		command = "xdg-open"
		if platform == "darwin" {
			command = "open"
		}
	}
	pending, err := nativeSkillWork(m, scope, func() (bool, error) {
		path, failure := nativeSandboxExecutable(native.Environment, command)
		if failure != nil && (platform == "windows" || platform == "win32") && filepath.Ext(command) == "" {
			for _, suffix := range strings.Split(native.Environment["PATHEXT"], ";") {
				if suffix != "" {
					path, failure = nativeSandboxExecutable(native.Environment, command+suffix)
					if failure == nil {
						break
					}
				}
			}
			if failure != nil {
				path, failure = nativeSandboxExecutable(native.Environment, command+".exe")
			}
		}
		if failure != nil {
			return false, nil
		}
		result, failure := worktree.NativeRun(ctx, worktree.ProcessRequest{Command: path, Arguments: args, CWD: native.CWD, Environment: native.Environment, IgnoreStdin: true})
		return failure == nil && result.Code == 0, nil
	})
	if err != nil {
		return false, err
	}
	return pending.Await(scope)
}
