package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"testing"
)

func mainRecipeArgs() []string {
	return []string{"--print", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json", "--include-partial-messages", "--replay-user-messages", "--tools", "Write", "--allowedTools", "Write", "--dangerously-skip-permissions", "--model", "fixture-model", "--thinking", "disabled", "--system-prompt", "real public system", "--max-turns", "4", "--setting-sources", "", "--session-id", "38f3a510-8df0-4e71-8e37-900ccb614602"}
}
func TestPublicPrintArgumentsUseCommanderAndMaxTurns(t *testing.T) {
	args := mainRecipeArgs()
	v, e := ParsePrintArguments(args, "fixture")
	if e != nil {
		t.Fatalf("parsed=%#v error=%v", cliargs.Parse(args, cliargs.Config{Version: "fixture"}), e)
	}
	if v.MaxTurns != 4 || v.SessionID == "" || len(v.Tools) != 1 || v.Tools[0] != "Write" || len(v.Sources.AllowedSettingSources) != 0 {
		t.Fatal(v)
	}
	args = append(args, "--resume", v.SessionID)
	if _, e = ParsePrintArguments(args, "fixture"); e == nil {
		t.Fatal("simultaneous SID must not select a prepared state")
	}
	args = append(mainRecipeArgs(), "--bare")
	if _, e = ParsePrintArguments(args, "fixture"); e == nil {
		t.Fatal("bare must not secretly gain Write")
	}
}
