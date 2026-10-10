package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

// Source systemInit and both print initialize projections admit an absent flag.
// These definitions exercise the production projectors, including order and hints.
func TestPrintCommandAbsentUserInvocableRemainsPublic(t *testing.T) {
	all := []printCommandDefinition{
		{name: "ordinary", description: "ordinary description", kind: "local"},
		{name: "internal", userFacingName: "public", description: "raw", hint: "<argument>", kind: "prompt", source: "bundled"},
	}
	system, e := protocol.EncodeNDJSON(projectSystemCommands(all))
	if e != nil {
		t.Fatal(e)
	}
	if string(system) != `[{"name":"ordinary","description":"ordinary description"},{"name":"internal","description":"raw","argumentHint":"<argument>"}]`+"\n" {
		t.Fatalf("absent userInvocable must remain in original system projection: %s", system)
	}
	control, e := protocol.EncodeNDJSON(projectControlCommands(all))
	if e != nil {
		t.Fatal(e)
	}
	if string(control) != `[{"name":"ordinary","description":"ordinary description","argumentHint":""},{"name":"public","description":"raw (bundled)","argumentHint":"<argument>"}]`+"\n" {
		t.Fatalf("absent userInvocable must remain in original control projection: %s", control)
	}
}

func TestPrintCommandOptionalBooleanCases(t *testing.T) {
	allow, deny := true, false
	all := []printCommandDefinition{
		{name: "default"},
		{name: "suppressed", userInvocable: &deny},
		{name: "explicit", userInvocable: &allow},
		{name: "null", userInvocable: nil},
	}
	system, control := projectSystemCommands(all), projectControlCommands(all)
	if len(system) != 3 || len(control) != 3 {
		t.Fatal(system, control)
	}
	for i, name := range []string{"default", "explicit", "null"} {
		if system[i].Name != name || control[i].Name != name {
			t.Fatalf("projection changed original order: %v %v", system, control)
		}
	}
	for _, command := range migratedPrintCommands() {
		if command.userInvocable != nil {
			t.Fatalf("shipped builtin %s invented absent Source flag", command.name)
		}
	}
}
