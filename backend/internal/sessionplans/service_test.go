package sessionplans

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPhysicalSlugRetryResumeCacheDirectoryAndEditedUTF16(t *testing.T) {
	root := t.TempDir()
	config, cwd := filepath.Join(root, "config"), filepath.Join(root, "work")
	_ = os.MkdirAll(cwd, 0700)
	_ = os.MkdirAll(filepath.Join(config, "plans"), 0700)
	owner := cooperative.New()
	defer owner.Close()
	state, e := bootstrapstate.New(owner, "actual-sid", cwd, func() (string, error) { return "next", nil })
	if e != nil {
		t.Fatal(e)
	}
	settings := protocol.NewJSONObject()
	draws := 0
	s, e := New(Options{Bootstrap: state, ConfigDir: config, CWD: cwd, InitialSettings: func() (*protocol.JSONObject, error) { return &settings, nil }, WordSlug: func() (string, error) {
		draws++
		if draws == 1 {
			return "taken", nil
		}
		return "fresh", nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(filepath.Join(config, "plans", "taken.md"), []byte("other session"), 0600)
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		path, e := s.Path(scope, "")
		if e != nil {
			return e
		}
		if path != filepath.Join(config, "plans", "fresh.md") || draws != 2 {
			t.Fatal(path, draws)
		}
		settings.Set("plansDirectory", json.RawMessage(`"elsewhere"`))
		again, e := s.Path(scope, "worker")
		if e != nil {
			return e
		}
		if again != filepath.Join(config, "plans", "fresh-agent-worker.md") {
			t.Fatal("directory memo/cache changed", again)
		}
		if e := s.WriteEdited(scope, "", json.RawMessage(`"edited \ud800"`)); e != nil {
			return e
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if string(data) != "edited �" {
			t.Fatal("Source UTF8 replacement lost", string(data))
		}
		in := protocol.NewJSONObject(protocol.JSONField{Name: "future", Value: json.RawMessage(`{"keep":true}`)})
		normalized, e := s.NormalizeExitInput(scope, in, "")
		if e != nil {
			return e
		}
		if normalized.String("plan") != "edited �" || normalized.String("planFilePath") != path || normalized.Get("future") == nil {
			t.Fatal("normalization did not read actual disk", normalized)
		}
		for _, candidate := range []string{path, filepath.Join(config, "plans", "fresh-agent-worker.md"), filepath.Join(config, "plans", "fresh-other.md")} {
			allowed, e := s.IsSessionFile(scope, candidate)
			if e != nil {
				return e
			}
			if !allowed {
				t.Fatal("Source lexical prefix rejected", candidate)
			}
		}
		allowed, e := s.IsSessionFile(scope, filepath.Join(config, "plans", "fresh", "..", "other.md"))
		if e != nil {
			return e
		}
		if allowed {
			t.Fatal("traversal crossed actual slug")
		}
		cache, e := state.PlanSlugCache(scope)
		if e != nil {
			return e
		}
		if e := cache.Set(scope, state.SessionID(), "restored-plan"); e != nil {
			return e
		}
		resumed, e := s.Path(scope, "")
		if e != nil {
			return e
		}
		if resumed != filepath.Join(config, "plans", "restored-plan.md") || draws != 2 {
			t.Fatal("resume ignored actual shared slug", resumed, draws)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestDirectoryOutsideProjectFallsBackAndMissingPlanDoesNotInjectFields(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "work")
	_ = os.MkdirAll(cwd, 0700)
	owner := cooperative.New()
	defer owner.Close()
	state, _ := bootstrapstate.New(owner, "sid", cwd, func() (string, error) { return "next", nil })
	settings := protocol.NewJSONObject(protocol.JSONField{Name: "plansDirectory", Value: json.RawMessage(`"../outside"`)})
	var errors []error
	s, e := New(Options{Bootstrap: state, ConfigDir: filepath.Join(root, "config"), CWD: cwd, InitialSettings: func() (*protocol.JSONObject, error) { return &settings, nil }, WordSlug: func() (string, error) { return "unique", nil }, OnError: func(e error) { errors = append(errors, e) }})
	if e != nil {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		path, e := s.Path(scope, "")
		if e != nil {
			return e
		}
		if path != filepath.Join(root, "config", "plans", "unique.md") || len(errors) != 1 {
			t.Fatal("outside settings did not follow Source fallback", path, errors)
		}
		in := protocol.NewJSONObject(protocol.JSONField{Name: "plan", Value: json.RawMessage(`"model supplied passthrough"`)})
		result, e := s.NormalizeExitInput(scope, in, "")
		if e != nil {
			return e
		}
		if result.String("plan") != "model supplied passthrough" || result.Get("planFilePath") != nil {
			t.Fatal("missing disk plan injected false fields", result)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
