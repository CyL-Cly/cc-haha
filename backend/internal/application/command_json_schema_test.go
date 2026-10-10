package application

import (
	"path/filepath"
	"testing"
)

func TestNativeCommandJSONSchemaRetainsOriginalStringForActualModeGate(t *testing.T) {
	for _, args := range [][]string{
		{"--print", "--json-schema", `{"type":"object"}`, "hello"},
		{"--print", `--json-schema={"type":"object"}`, "hello"},
		{"--json-schema", `{malformed`, "interactive prompt"},
		{"--print", "--json-schema", "false", "hello"},
	} {
		work := t.TempDir()
		command, parsed, e := ParseCommand(args, "fixture", work, filepath.Join(work, "tmp"))
		if e != nil || parsed.Parsed == nil {
			t.Fatal(args, e, parsed.Stderr)
		}
		value, ok := parsed.Parsed.Options["jsonSchema"].(string)
		if !ok || command.Arguments.JSONSchema != value {
			t.Fatalf("actual CLI schema string was changed: %q -> %q", value, command.Arguments.JSONSchema)
		}
		// Source's interactive gate ignores even invalid JSON. Parsing the flag
		// here must not manufacture a validation error before MainPrint mode.
		if args[0] == "--json-schema" && command.Print {
			t.Fatal("interactive option enabled print")
		}
	}
}
