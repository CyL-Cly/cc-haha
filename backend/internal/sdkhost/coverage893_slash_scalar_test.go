package sdkhost

import (
	"encoding/json"
	"testing"
)

// Source getCommandNameAndArgs handles (MCP) at scalar parse time; this does
// not claim the unmigrated MCP command handler is supported.
func TestCoverage893SlashScalarRetainsArgumentsAndMCPMarker(t *testing.T) {
	for _, tc := range []struct {
		input, name, args string
		slash             bool
	}{
		{`"/cost   private 😀"`, "cost", "  private 😀", true}, {`" /cost private"`, "", "", false}, {`"/inspect (MCP) private 😀"`, "inspect (MCP)", "private 😀", true}, {`"/cost "`, "cost", "", true},
	} {
		n, a, s, e := slashParts(json.RawMessage(tc.input))
		if e != nil || n != tc.name || a != tc.args || s != tc.slash {
			t.Fatal(tc, n, a, s, e)
		}
	}
}
