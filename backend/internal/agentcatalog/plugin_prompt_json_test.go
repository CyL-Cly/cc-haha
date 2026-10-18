package agentcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestPluginPromptJSONCapturesRawOptionsApartFromMutableMetadataAndAppendsMemoryOnce(t *testing.T) {
	f := newFixture(t)
	s := f.service()
	raw := json.RawMessage(`"left\ud800right"`)
	d, err := PluginDefinitionJSON("/private/fixture.md", "sample", "sample@fixture", nil, []byte("---\nmemory: project\n---\noriginal"), true, func(json.RawMessage) (json.RawMessage, error) { return raw, nil })
	if err != nil {
		t.Fatal(err)
	}
	// A returned metadata record and a substitution callback's buffer are not
	// the original getSystemPrompt closure's captured immutable string.
	d.Fields.Set("rawSystemPrompt", json.RawMessage(`"mutated metadata"`))
	raw[1] = 'X'
	calls := 0
	s.options.AutoMemoryEnabled = func(*cooperative.Scope) (bool, error) { return true, nil }
	s.options.MemoryPrompt = func(*cooperative.Scope, string, string) (string, error) { calls++; return "MEMORY", nil }
	if err := f.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for range 2 {
			got, err := s.PromptJSON(scope, *d, agentbuiltinmetadata.GuideContext{})
			if err != nil {
				return err
			}
			if string(got) != `"left\ud800right\n\nMEMORY"` {
				t.Errorf("captured prompt/memory changed: %s", got)
			}
			got[1] = 'Y'
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("memory producer was duplicated", calls)
	}
	identity := errors.New("actual memory failure")
	s.options.MemoryPrompt = func(*cooperative.Scope, string, string) (string, error) { return "", identity }
	if err := f.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := s.PromptJSON(scope, *d, agentbuiltinmetadata.GuideContext{})
		return err
	}); err != identity {
		t.Fatal("memory failure identity changed", err)
	}
	if d, err := PluginDefinitionJSON("/private/fixture.md", "sample", "sample@fixture", nil, []byte("original"), false, func(json.RawMessage) (json.RawMessage, error) { return nil, identity }); d != nil || err != identity {
		t.Fatal("substitution failure was swallowed", d, err)
	}
}
