package clibootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

// The pre-CLI process has the original pending async-hook registry and input
// notification queue, but no REPL, SDK output handler or model consumer yet.
// These are process resources on its one outside-REPL actor, not a Harness.
type nativeTmuxHookRuntime struct {
	owner            *cooperative.Owner
	registry         *queryhooks.AsyncRegistry
	n                *NativeBoundary
	env              map[string]string
	sid, platform    string
	work             *sync.WaitGroup
	responses        []protocol.JSONObject
	notifications    []string
	environmentEpoch uint64
}

func newNativeTmuxHookRuntime(owner *cooperative.Owner, n *NativeBoundary, env map[string]string, sid, platform string, work *sync.WaitGroup) (*nativeTmuxHookRuntime, error) {
	h := &nativeTmuxHookRuntime{owner: owner, n: n, env: env, sid: sid, platform: platform, work: work}
	var e error
	h.registry, e = queryhooks.NewAsyncRegistry(queryhooks.AsyncRegistryOptions{Owner: owner, NowMillis: n.NowMillis, Event: func(scope *cooperative.Scope, hook queryhooks.AsyncHook, out queryhooks.CommandOutput, outcome string) error {
		if scope == nil || !scope.BelongsTo(owner) {
			return cooperative.ErrRestricted
		}
		// Original emitHookResponse still receives completion; WorktreeCreate is
		// filtered by its default includeHookEvents=false bus before any stdout.
		h.responses = append(h.responses, protocol.NewJSONObject(protocol.JSONField{Name: "hookId", Value: quoteMain(hook.HookID)}, protocol.JSONField{Name: "hookName", Value: quoteMain(hook.HookName)}, protocol.JSONField{Name: "hookEvent", Value: quoteMain(hook.HookEvent)}, protocol.JSONField{Name: "output", Value: quoteMain(out.Stdout + out.Stderr)}, protocol.JSONField{Name: "stdout", Value: quoteMain(out.Stdout)}, protocol.JSONField{Name: "stderr", Value: quoteMain(out.Stderr)}, protocol.JSONField{Name: "exitCode", Value: json.RawMessage(strconv.Itoa(out.Code))}, protocol.JSONField{Name: "outcome", Value: quoteMain(outcome)}))
		return nil
	}, Notify: func(scope *cooperative.Scope, text string) error {
		if scope == nil || !scope.BelongsTo(owner) {
			return cooperative.ErrRestricted
		}
		h.notifications = append(h.notifications, text)
		return nil
	}, Invalidate: func() { h.environmentEpoch++ }})
	return h, e
}
func (h *nativeTmuxHookRuntime) outputDirectory() string {
	base := h.env["CLAUDE_CODE_TMPDIR"]
	if base == "" {
		base = "/tmp"
	}
	if resolved, e := filepath.EvalSymlinks(base); e == nil {
		base = resolved
	}
	return filepath.Join(base, "claude-"+strconv.Itoa(os.Getuid()), projectpath.Sanitize(h.n.CWD), h.sid, "tasks")
}
func (h *nativeTmuxHookRuntime) Run(ctx context.Context, call queryhooks.Context, hook, input protocol.JSONObject) (queryhooks.CommandOutput, error) {
	if call.Scope == nil || !call.Scope.BelongsTo(h.owner) {
		return queryhooks.CommandOutput{}, cooperative.ErrRestricted
	}
	hook, input = hook.Clone(), input.Clone()
	future, e := tmuxNativeWork(h.owner, call.Scope, h.work, func() (queryhooks.CommandOutput, error) {
		return queryhooks.RunCommandProcess(ctx, queryhooks.CommandProcessRequest{AllowAsync: true, OutputDirectory: h.outputDirectory(), Hook: hook, Input: input, CWD: h.n.CWD, OriginalCWD: h.n.CWD, ProjectRoot: h.n.CWD, Platform: h.platform, Environment: h.env, NowMillis: h.n.NowMillis, ShellResolver: queryhooks.NewCommandShellResolver(h.env, h.platform)})
	})
	if e != nil {
		return queryhooks.CommandOutput{}, e
	}
	out, e := future.Await(call.Scope)
	if e != nil {
		return out, e
	}
	if out.Backgrounded {
		e = h.registry.Register(call.Scope, queryhooks.AsyncHook{ProcessID: out.AsyncProcessID, HookID: call.HookID, HookName: call.HookName, HookEvent: call.HookEvent, Command: hook.String("command"), Timeout: out.AsyncTimeout, Process: out.AsyncProcess, Rewake: out.AsyncRewake})
		if e != nil {
			out.AsyncProcess.Kill()
			out.AsyncProcess.Join()
		}
	}
	return out, e
}
func (h *nativeTmuxHookRuntime) Close(ctx context.Context) error { return h.registry.Close(ctx) }
