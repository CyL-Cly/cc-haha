package builtinwrite

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestFileCacheCrossOwnerSnapshotClonesContentsRangesAndRecency(t *testing.T) {
	parentOwner, childOwner := cooperative.New(), cooperative.New()
	defer parentOwner.Close()
	defer childOwner.Close()
	parent, err := NewFileStateCache(parentOwner, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	child, err := NewFileStateCache(childOwner, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	offset, limit := float64(2), float64(3)
	var snapshot []CachedFile
	if err := parentOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := parent.Set(scope, "/a", FileState{Content: json.RawMessage(`"a"`), Timestamp: math.NaN(), Offset: &offset, Limit: &limit, PartialView: true}); err != nil {
			return err
		}
		if err := parent.Set(scope, "/b", FileState{Content: json.RawMessage(`"b"`), Timestamp: 7}); err != nil {
			return err
		}
		var err error
		snapshot, err = parent.Export(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 2 || snapshot[0].Path != "/b" || snapshot[1].Path != "/a" {
		t.Fatalf("MRU snapshot: %+v", snapshot)
	}
	if err := childOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := child.Set(scope, "/stale", FileState{Content: json.RawMessage(`"stale"`)}); err != nil {
			return err
		}
		return child.Import(scope, snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	snapshot[1].State.Content[1] = 'z'
	*snapshot[1].State.Offset = 99
	for _, pair := range []struct {
		owner *cooperative.Owner
		cache *FileStateCache
	}{{parentOwner, parent}, {childOwner, child}} {
		if err := pair.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			rows, err := pair.cache.Export(scope)
			if err != nil {
				return err
			}
			if len(rows) != 2 || rows[0].Path != "/b" {
				t.Fatalf("clone changed recency: %+v", rows)
			}
			state, exists, err := pair.cache.Get(scope, "/a")
			if err != nil {
				return err
			}
			if !exists || string(state.Content) != `"a"` || !math.IsNaN(state.Timestamp) || !state.PartialView || state.Offset == nil || *state.Offset != 2 || state.Limit == nil || *state.Limit != 3 {
				t.Fatalf("borrowed or lost state: %+v", state)
			}
			_, exists, err = pair.cache.Get(scope, "/stale")
			if exists {
				t.Fatal("import kept prior child cache")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := parentOwner.Run(context.Background(), func(scope *cooperative.Scope) error { return child.Import(scope, nil) }); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatalf("foreign owner import: %v", err)
	}
}
