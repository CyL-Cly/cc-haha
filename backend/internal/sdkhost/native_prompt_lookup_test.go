package sdkhost

import (
	"context"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestBundledPromptWithoutDisplayNameDoesNotResolveEmptySlash(t *testing.T) {
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote("bundled")}, protocol.JSONField{Name: "type", Value: quote("prompt")}, protocol.JSONField{Name: "source", Value: quote("bundled")}, protocol.JSONField{Name: "loadedFrom", Value: quote("bundled")})
	definition, e := skillcommands.NewBundled(metadata, "", func(context.Context, *string) (string, error) { return "actual prompt", nil })
	if e != nil {
		t.Fatal(e)
	}
	registry := &PrintRegistry{prompts: []skillcommands.Definition{definition}}
	if _, exists := registry.Prompt(""); exists {
		t.Fatal("empty slash resolved an absent display-name property")
	}
	if found, exists := registry.Prompt("bundled"); !exists || found.Name() != "bundled" {
		t.Fatal("real bundled name did not resolve")
	}
}
