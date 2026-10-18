package sessionplans

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitForkSlugUsesTargetCacheBeforeSwitchAndRetainsOriginal(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	root := t.TempDir()
	state, e := bootstrapstate.New(owner, "parent", root, func() (string, error) { return "next", nil })
	if e != nil {
		t.Fatal(e)
	}
	settings := protocol.NewJSONObject()
	draws := 0
	s, e := New(Options{Bootstrap: state, ConfigDir: filepath.Join(root, "config"), CWD: root, InitialSettings: func() (*protocol.JSONObject, error) { return &settings, nil }, WordSlug: func() (string, error) {
		draws++
		if draws == 1 {
			return "existing", nil
		}
		return "fork-unique", nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	if e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		cache, e := state.PlanSlugCache(scope)
		if e != nil {
			return e
		}
		if e = cache.Set(scope, "parent", "parent-plan"); e != nil {
			return e
		}
		dir, e := s.Directory(scope)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(dir, "existing.md"), []byte("another session"), 0600); e != nil {
			return e
		}
		fork, e := s.SlugForSession(scope, "fork")
		if e != nil {
			return e
		}
		parent, e := s.Slug(scope)
		if e != nil {
			return e
		}
		again, e := s.SlugForSession(scope, "fork")
		if e != nil {
			return e
		}
		if fork != "fork-unique" || again != fork || parent != "parent-plan" || state.SessionID() != "parent" || draws != 2 {
			t.Fatal(fork, again, parent, state.SessionID(), draws)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if e = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := s.SlugForSession(scope, "foreign")
		if !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal(e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
