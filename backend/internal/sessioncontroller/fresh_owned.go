package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

const FreshPrintProfile = "source-loadInitialMessages-normal-print-v1"

const FreshBareSDKProfile = "source-loadInitialMessages-bare-startup-v1"

const FreshRemoteCLIProfile = "source-main-remote-teleport-startup-v1"

// BeginActivateFreshOwned is the actual no-resume loadInitialMessages branch.
// Source processSessionStartHooks is async and returns [] under bare; its await
// then fulfills loadInitialMessages. This returns the same named two-producer
// graph, not a synthetic LogOption routed through the resume reducer.
func (c *Controller) BeginActivateFreshOwned(scope *cooperative.Scope, profile string) (*ownerasync.OwnerFuture[struct{}], error) {
	if c == nil || c.options.Bootstrap == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return nil, err
	}
	normal := profile == FreshPrintProfile && c.options.PublicPrint && c.options.StartupHooks != nil
	remote := profile == FreshRemoteCLIProfile && c.options.NativeRemoteCLI
	if !normal && !remote && (profile != FreshBareSDKProfile || c.options.ResumeOptions.Environment["CLAUDE_CODE_SIMPLE"] != "1") {
		return nil, capability("fresh non-bare SessionStart producer")
	}
	if c.active {
		return nil, capability("repeated headless startup activation")
	}
	owner := c.options.Bootstrap.Owner()
	hooks, err := ownerasync.NewOwnerFuture[struct{}](owner)
	if normal {
		hooks, err = c.options.StartupHooks.BeginSessionStart(scope, owner, c.SessionID(), "startup")
	}
	if err != nil {
		return nil, err
	}
	if !normal {
		err = hooks.Resolve(scope, struct{}{})
	}
	if err != nil {
		return nil, err
	}
	// This reaction is loadInitialMessages's real await of the bare hook result.
	return hooks.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			return failure
		}
		if err := c.options.Bootstrap.CheckScope(next); err != nil {
			return err
		}
		if c.active {
			return capability("repeated headless startup activation")
		}
		c.messages = nil
		if normal {
			result, err := c.options.StartupHooks.TakeSessionStartResult(next, c.SessionID())
			if err != nil {
				return err
			}
			for _, message := range result.Messages {
				copy := message
				c.messages = append(c.messages, &copy)
			}
		}
		c.readCache = newReadCache(headlessReadCacheEntries)
		c.active = true
		c.freshOwned = true
		c.options.OnPhase("startup:fresh-return")
		return nil
	})
}
