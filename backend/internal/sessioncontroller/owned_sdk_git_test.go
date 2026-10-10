package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
)

func TestOwnedSDKGitFreshDoesNotContinueAndSamplesActualAppState(t *testing.T) {
	c, _, owner := boundController(t)
	c.options.ResumeOptions.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	phases := []string{}
	c.options.OnPhase = func(s string) { phases = append(phases, s) }
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		// The old public nil resume still means --continue, not a fresh empty log.
		if e := c.ActivateOwned(scope, Activation{}); e == nil || e.Error() != "No conversation found" {
			return fmt.Errorf("nil resume changed: %v", e)
		}
		future, e := c.BeginActivateFreshOwned(scope, FreshBareSDKProfile)
		if e != nil {
			return e
		}
		if c.active {
			return errors.New("loadInitialMessages await skipped")
		}
		if _, e = c.ResolvedFastModeOnOwner(scope); !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("inactive mode sampled: %v", e)
		}
		if _, e = future.Await(scope); e != nil {
			return e
		}
		if !c.active || !c.freshOwned || len(c.messages) != 0 || c.result != nil {
			return errors.New("fresh startup used reducer/resume history")
		}
		mode, e := c.ResolvedFastModeOnOwner(scope)
		if e != nil || mode == nil || *mode {
			return fmt.Errorf("default AppState not false: %v", e)
		}
		*mode = true // returned value is not the mutable store.
		if *c.appState.fastMode {
			return errors.New("sample leaked store pointer")
		}
		if _, e = c.BeginActivateFreshOwned(scope, FreshBareSDKProfile); e == nil {
			return errors.New("repeated activation admitted")
		}
		c.appState.fastMode = nil
		if _, e = c.ResolvedFastModeOnOwner(scope); e == nil {
			return errors.New("unknown mode admitted")
		}
		active := true
		c.appState.fastMode = &active
		if _, e = c.ResolvedFastModeOnOwner(scope); e == nil {
			return errors.New("active mode admitted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(phases) != 3 || phases[0] != "resume:start" || phases[1] != "resume:return" || phases[2] != "startup:fresh-return" {
		t.Fatalf("unexpected lifecycle %v", phases)
	}
}

func TestOwnedSDKGitFreshScopeAndProfileGuards(t *testing.T) {
	c, _, owner := boundController(t)
	c.options.ResumeOptions.Environment["CLAUDE_CODE_SIMPLE"] = "0"
	foreign := cooperative.New()
	defer foreign.Close()
	if _, e := c.ResolvedFastModeOnOwner(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := c.BeginActivateFreshOwned(nil, FreshBareSDKProfile); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := foreign.Run(context.Background(), func(s *cooperative.Scope) error {
		if _, e := c.ResolvedFastModeOnOwner(s); !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign mode %v", e)
		}
		_, e := c.BeginActivateFreshOwned(s, FreshBareSDKProfile)
		if !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var stale *cooperative.Scope
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		stale = s
		for _, profile := range []string{"", FreshBareSDKProfile} {
			_, e := c.BeginActivateFreshOwned(s, profile)
			if e == nil {
				return errors.New("unproved startup profile admitted")
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.ResolvedFastModeOnOwner(stale); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := c.BeginActivateFreshOwned(stale, FreshBareSDKProfile); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	var nilController *Controller
	if _, e := nilController.BeginActivateFreshOwned(nil, FreshBareSDKProfile); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
}

func TestOwnedSDKGitExplicitRecordProfiles(t *testing.T) {
	c, state, owner := boundController(t)
	if e := c.ValidateSDKRecordProfile(); e == nil {
		t.Fatal("bound nil fallback admitted")
	}
	c.options.RecordBranchProfile = LegacySyncRecordProfile
	if e := c.ValidateSDKRecordProfile(); e != nil {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		opts, e := c.recordOptions(s)
		if e != nil {
			return e
		}
		if opts.Branch != nil || opts.CWDContext != nil {
			return errors.New("legacy profile got native branch")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	binding, e := NewOwnedGitRecordBranch(state, actualOwnedGitService(t, state, owner))
	if e != nil {
		t.Fatal(e)
	}
	c.options.BeginOwnedRecordBranch = binding
	if e := c.ValidateSDKRecordProfile(); e == nil {
		t.Fatal("two branch domains admitted")
	}
	c.options.RecordBranchProfile = OwnedGitRecordProfile
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		opts, e := c.recordOptions(s)
		if e != nil {
			return e
		}
		if opts.Branch == nil || opts.CWDContext == nil {
			return errors.New("record lost context")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	c.options.BeginOwnedRecordBranch = nil
	if e := c.ValidateSDKRecordProfile(); e == nil {
		t.Fatal("missing native capability admitted")
	}
	c.options.RecordBranchProfile = "unknown"
	if e := c.ValidateSDKRecordProfile(); e == nil {
		t.Fatal("unknown profile admitted")
	}
	if _, e := NewOwnedGitRecordBranch(state, nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := (*Controller)(nil).ValidateSDKRecordProfile(); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
}

func TestOwnedSDKGitRecordDistinguishesCapabilitiesFromNativeIO(t *testing.T) {
	for _, fault := range []error{&gitbranch.CapabilityError{Boundary: "active scroll"}, cooperative.ErrRestricted, cooperative.ErrClosed, context.Canceled, context.DeadlineExceeded, errors.New("native HEAD ENOENT")} {
		t.Run(fault.Error(), func(t *testing.T) {
			owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
			done := make(chan struct{})
			close(done)
			f := startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("original-user", "content")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }, Branch: func(*cooperative.Scope) (RecordBranchFuture, error) {
				return &resolvedRecordBranch{done, "", fault}, nil
			}})
			if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
				_, e := f.Await(s)
				if fault.Error() == "native HEAD ENOENT" {
					return e
				}
				if !errors.Is(e, fault) {
					return fmt.Errorf("capability swallowed %v", e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if fault.Error() == "native HEAD ENOENT" {
				if e := flushRecord(t, owner, pending).Wait(context.Background()); e != nil {
					t.Fatal(e)
				}
				if writer.path == "" {
					t.Fatal("ordinary IO prevented transcript")
				}
			} else if writer.path != "" && len(writer.members) > 0 {
				t.Fatal("capability made record visible")
			}
		})
	}
}
