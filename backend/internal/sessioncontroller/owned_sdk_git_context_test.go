package sessioncontroller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
)

func TestOwnedSDKGitBranchUsesOneServiceAndOriginalContext(t *testing.T) {
	c, state, owner := boundController(t)
	home := c.options.WriterOptions.CWD
	gitDir := filepath.Join(home, ".git")
	if e := os.MkdirAll(gitDir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/original-live\n"), 0600); e != nil {
		t.Fatal(e)
	}
	var service *gitbranch.Service
	cleanupCalls := 0
	options := gitbranch.Options{Owner: owner, CWD: func() string { panic("original getter bypassed context") }, Now: func() int64 { return time.Now().UnixMilli() }, StatSync: os.Stat, Stat: os.Stat, ReadFile: os.ReadFile, ScrollDraining: func() bool { return false }, WatchFile: func(string, time.Duration, func()) (func(), error) { return func() {}, nil }, RegisterCleanupOwned: func(s *cooperative.Scope, fn func(*cooperative.Scope) error) error {
		if !s.BelongsTo(owner) || fn == nil {
			return cooperative.ErrRestricted
		}
		cleanupCalls++
		return nil
	}}
	var e error
	service, e = gitbranch.New(options)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	branch, e := NewOwnedGitRecordBranch(state, service)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewOwnedGitRecordBranch(nil, service); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
		token, e := state.Context(s)
		if e != nil {
			return e
		}
		if _, e = branch.Begin(s, nil); !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("nil context %v", e)
		}
		c.options.RecordBranchProfile = OwnedGitRecordProfile
		c.options.BeginOwnedRecordBranch = branch
		opts, e := c.recordOptions(s)
		if e != nil {
			return e
		}
		for index := 0; index < 2; index++ {
			result, e := opts.Branch(s)
			if e != nil {
				return e
			}
			original, ok := result.(*gitbranch.BranchFuture)
			if !ok {
				return errors.New("not original Git future")
			}
			value, e := original.Await(s)
			if e != nil {
				return e
			}
			if value != "original-live" {
				return fmt.Errorf("native branch %q", value)
			}
		}
		child := filepath.Join(home, "descendant")
		if e = state.SetCWD(s, child); e != nil {
			return e
		}
		if value, e := token.GetCWD(s); e != nil || value != child {
			return fmt.Errorf("default context not live %q %v", value, e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if cleanupCalls != 1 {
		t.Fatal("one process service was replaced on hot call", cleanupCalls)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	foreignOptions := options
	foreignOptions.Owner = foreign
	foreignService, e := gitbranch.New(foreignOptions)
	if e != nil {
		t.Fatal(e)
	}
	foreignBranch, e := NewOwnedGitRecordBranch(state, foreignService)
	var unavailable *CapabilityError
	if !errors.As(e, &unavailable) || foreignBranch != nil {
		t.Fatal("foreign binding factory admitted", e)
	}
	if e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
		token, e := state.Context(s)
		if e != nil {
			return e
		}
		_, e = foreignService.BranchWithCWD(s, token.GetCWD)
		if !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign Git service %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = foreignService.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = foreign.Run(context.Background(), func(s *cooperative.Scope) error {
		_, e := branch.Begin(s, nil)
		if !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign call %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	// The own-canonical scope cannot lend a foreign async-local token to Git.
	otherState, e := bootstrapstate.New(owner, "other", home, func() (string, error) { return "id", nil })
	if e != nil {
		t.Fatal(e)
	}
	if e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
		token, e := otherState.Context(s)
		if e != nil {
			return e
		}
		_, e = branch.Begin(s, token)
		return e
	}); e == nil {
		t.Fatal("foreign State token admitted")
	}
}
