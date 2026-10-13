package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
	"testing"
)

func TestDynamicAgentToolsRetireSameToolsInRequestAndSearchWithoutMutatingCapturedRound(t *testing.T) {
	defs := []toolschema.Definition{{Name: "Read"}, {Name: "TaskStop"}}
	first := requestcontext.Context{Tools: []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote("Read")}), protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote("TaskStop")})}, Host: requestcontext.Host{NativeToolDefinitions: &defs, ToolSearchCatalogue: &toolsearch.Catalogue{Tools: []toolsearch.Tool{{Name: json.RawMessage(`"Read"`)}, {Name: json.RawMessage(`"TaskStop"`)}}}}}
	background := false
	h := &Host{options: Options{Print: &PrintOptions{ToolAllowedNow: func(name string) bool { return !background || name == "Read" }}}}
	before := h.filterDynamicAgentTools(first)
	if len(before.Tools) != 2 {
		t.Fatal("foreground tools lost")
	}
	background = true
	after := h.filterDynamicAgentTools(first)
	if len(after.Tools) != 1 || after.Tools[0].String("name") != "Read" || len(*after.Host.NativeToolDefinitions) != 1 || len(after.Host.ToolSearchCatalogue.Tools) != 1 {
		t.Fatal("retired declarations still visible", after)
	}
	if len(first.Tools) != 2 || len(*first.Host.NativeToolDefinitions) != 2 || len(first.Host.ToolSearchCatalogue.Tools) != 2 {
		t.Fatal("already admitted request changed")
	}
}
