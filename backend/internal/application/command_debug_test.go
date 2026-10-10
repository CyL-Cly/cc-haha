package application

import (
	"reflect"
	"testing"
)

func TestPublicDebugRawArgvSurvivesCommanderBooleanCoercionWithoutEnablingOtherFlags(t *testing.T) {
	for _, tokens := range [][]string{{"--debug-file", "private.log"}, {"--debug-file=private.log"}, {"-d2e"}, {"-dapi"}, {"--debug=api,hooks"}, {"--debug", "api"}, {"--debug-to-stderr"}} {
		args := append(append([]string{}, tokens...), "--print", "--output-format", "json", "prompt")
		command, parsed, e := ParseCommand(args, "fixture", t.TempDir(), t.TempDir())
		if e != nil || parsed.Parsed == nil || !reflect.DeepEqual(command.Arguments.DebugArguments, args) || !command.Arguments.DebugFormatted {
			t.Fatalf("raw debug arguments/coerced parser bind tokens%v: %v %+v", tokens, e, command.Arguments)
		}
		args[0] = "changed"
		if command.Arguments.DebugArguments[0] == "changed" {
			t.Fatal("caller mutation changed sink raw argv")
		}
	}
	command, _, e := ParseCommand([]string{"--debug", "--print", "prompt"}, "fixture", t.TempDir(), t.TempDir())
	if e != nil || command.Arguments.DebugFormatted {
		t.Fatal("plain terminal text incorrectly JSON-formatted debug", e)
	}
	if _, _, e := ParseCommand([]string{"--mcp-debug", "--print", "prompt"}, "fixture", t.TempDir(), t.TempDir()); e == nil {
		t.Fatal("debug admission widened deprecated MCP producer guard")
	}
}
