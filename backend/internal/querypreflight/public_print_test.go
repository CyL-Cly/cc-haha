package querypreflight

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"testing"
)

func TestPublicPrintBypassUsesOriginalModel(t *testing.T) {
	s, _, _, _, _ := testServices(t)
	in := base(t)
	in.PermissionMode = "bypassPermissions"
	in.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	got, err := s.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Source model.ts bypass preserves main model: %v", err)
	}
	if got.CurrentModel != in.MainLoopModel {
		t.Fatal(got.CurrentModel)
	}
}

func TestFixedPrefixUsesActualNonemptyToolSchemaWithoutRenderedPrompt(t *testing.T) {
	in := base(t)
	empty, e := fixedEstimate(in)
	if e != nil {
		t.Fatal(e)
	}
	d, e := toolschema.NativeBuiltinDefinition("Write")
	if e != nil {
		t.Fatal(e)
	}
	in.Tools = []toolschema.Definition{d}
	in.ToolCount = 1
	withTool, e := fixedEstimate(in)
	if e != nil || withTool <= empty {
		t.Fatal(withTool, empty, e)
	}
	d.Description = json.RawMessage(`"different description excluded from autoCompact"`)
	d.Prompt = json.RawMessage(`"different prompt excluded from autoCompact"`)
	in.Tools = []toolschema.Definition{d}
	same, e := fixedEstimate(in)
	if e != nil || same != withTool {
		t.Fatal("fixed schema budget accidentally uses rendered/cached descriptions", same, withTool, e)
	}
	s, _, _, _, _ := testServices(t)
	in.ToolCount = 2
	if _, e = s.Run(context.Background(), in); e == nil {
		t.Fatal("nonempty registry provenance count mismatch erased")
	}
}
