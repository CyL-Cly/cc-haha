package systemprompt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func rawPrompt(parts []json.RawMessage) []byte { data, _ := json.Marshal(parts); return data }
func TestAdditionalWorkingDirectoryInputRefreshesActualEnvironmentSection(t *testing.T) {
	s, err := New(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{"/private/explicit-workspace"}
	s.SetAdditionalWorkingDirectories(roots)
	roots[0] = "/private/mutated-caller"
	after, err := s.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawPrompt(before)), "explicit-workspace") || !strings.Contains(string(rawPrompt(after)), "explicit-workspace") || strings.Contains(string(rawPrompt(after)), "mutated-caller") {
		t.Fatal("current permission directories not consumed", string(rawPrompt(after)))
	}
	s.SetAdditionalWorkingDirectories(nil)
	removed, err := s.Resolve(context.Background(), nil)
	if err != nil || strings.Contains(string(rawPrompt(removed)), "explicit-workspace") {
		t.Fatal("removed directory cached", err)
	}
}
