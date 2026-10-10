package sdkhost

import (
	"os"
	"path/filepath"
	"testing"
)

// Source commands/cost/index.ts supports noninteractive; clear/index.ts does not.
// Exercise the actual initial registry, never a captured output array.
func TestInitialPrintRegistryCostIsRunnableMetadataAndClearIsHeadlessFiltered(t *testing.T) {
	root := t.TempDir()
	home, cwd := filepath.Join(root, "home"), filepath.Join(root, "workspace")
	if e := os.MkdirAll(home, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(cwd, 0700); e != nil {
		t.Fatal(e)
	}
	registry, e := NewPrintRegistry(PrintRegistryOptions{Home: home, CWD: cwd, Stat: os.Stat, ReadDir: os.ReadDir, EntryPoint: "sdk-cli", UserType: "external"})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, command := range registry.Catalog().Commands {
		if command.Name == "cost" {
			found = command.Description == "Show the total cost and duration of the current session"
		}
		if command.Name == "clear" {
			t.Fatal("initial SDK headless registry exposed interactive clear")
		}
	}
	if !found {
		t.Fatal("Source shipped headless /cost missing from production registry")
	}
	if registry.FullInitParityAccepted() {
		t.Fatal("finite catalog cannot prove whole init")
	}
}

func TestRegistryRefreshPreservesSourceHostDifferenceAndDistinctInitProjection(t *testing.T) {
	registry := &PrintRegistry{commands: filterHeadlessCommands(migratedPrintCommands())}
	for _, c := range registry.commands {
		if c.name == "clear" {
			t.Fatal("initial headless filtering lost")
		}
	}
	registry.RefreshMigratedCommands()
	clear := false
	for _, c := range registry.Catalog().Commands {
		if c.Name == "clear" {
			clear = true
		}
	}
	if !clear {
		t.Fatal("Source post-plugin refresh is deliberately unfiltered")
	}
	allow, deny := true, false
	definition := []printCommandDefinition{{name: "internal", userFacingName: "public", description: "Raw description", hint: "", kind: "prompt", source: "bundled", userInvocable: &allow}, {name: "hidden", kind: "prompt", userInvocable: &deny}}
	system := projectSystemCommands(definition)
	control := projectControlCommands(definition)
	if len(system) != 1 || system[0].Name != "internal" || system[0].Description != "Raw description" || len(control) != 1 || control[0].Name != "public" || control[0].Description != "Raw description (bundled)" {
		t.Fatal(system, control)
	}
	if registry.FullInitParityAccepted() {
		t.Fatal("partial metadata cannot accept whole init")
	}
}

func TestPrintRegistryActualEnabledGuardIsReevaluatedAtRefresh(t *testing.T) {
	flag := "true"
	registry := &PrintRegistry{environment: func(k string) string {
		if k == "DISABLE_COMPACT" {
			return flag
		}
		return ""
	}}
	registry.RefreshMigratedCommands()
	for _, c := range registry.Catalog().Commands {
		if c.Name == "compact" {
			t.Fatal("disabled compact silently enabled")
		}
	}
	flag = "false"
	registry.RefreshMigratedCommands()
	found := false
	for _, c := range registry.Catalog().Commands {
		if c.Name == "compact" {
			found = true
		}
	}
	if !found {
		t.Fatal("Source fresh isEnabled guard was cached as metadata")
	}
}
func TestCostParserAndUnsupportedHandlersRemainExplicit(t *testing.T) {
	h := &Host{options: Options{Print: &PrintOptions{Registry: &PrintRegistry{commands: filterHeadlessCommands(migratedPrintCommands())}}}}
	for _, input := range []string{"/context", "/clear", "/review", "/unknown", "/cost\targ"} {
		if e := h.validatePrintUserInput(quote(input)); e == nil {
			t.Fatalf("unmigrated handler %q admitted", input)
		}
	}
	if e := h.validatePrintUserInput(quote("/cost  ignored")); e != nil {
		t.Fatal(e)
	}
	_, args, slash, e := slashParts(quote("/cost  ignored"))
	if e != nil || !slash || args != " ignored" {
		t.Fatal(args, slash, e)
	}
	_, _, slash, e = slashParts(quote(" /cost"))
	if e != nil || slash {
		t.Fatal("Source outer startsWith gate must not trim ordinary leading spaces")
	}
}
