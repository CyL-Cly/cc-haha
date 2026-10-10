package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCoverHasNoCountersForRealPackageInitializerCalls(t *testing.T) {
	dir := t.TempDir()
	// Neither AST parsing nor the real instrumenter executes this initializer.
	// The malformed regex would panic if this application's code were run.
	source := []byte("package fixture\nimport \"regexp\"\nvar parsed = regexp.MustCompile(\"(\")\n")
	file := filepath.Join(dir, "initializer.go")
	if err := os.WriteFile(file, source, 0600); err != nil {
		t.Fatal(err)
	}
	row, err := inspect("backend/initializer.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if row.DeclarationOnly || len(row.Executable) != 1 || row.Executable[0].Kind != "initializer-call" || row.Executable[0].Line != 3 {
		t.Fatalf("real executable initializer misclassified: %+v", row)
	}
	count, err := compilerCounters(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("actual Go cover unexpectedly produced %d package-initializer counters", count)
	}
}

func TestCoverCountsBodiesWhileStaticTablesRemainDeclarations(t *testing.T) {
	for _, fixture := range []struct {
		name, source string
		declaration  bool
		counters     int
	}{
		{"static", "package fixture\nvar data = map[string]int{\"a\":1}\n", true, 0},
		{"function", "package fixture\nfunc value() int { return 1 }\n", false, 1},
		{"closure", "package fixture\nvar value = func() int { return 1 }\n", false, 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "source.go")
			if err := os.WriteFile(file, []byte(fixture.source), 0600); err != nil {
				t.Fatal(err)
			}
			row, err := inspect("backend/source.go", []byte(fixture.source))
			if err != nil {
				t.Fatal(err)
			}
			count, err := compilerCounters(file, dir)
			if err != nil {
				t.Fatal(err)
			}
			if row.DeclarationOnly != fixture.declaration || count != fixture.counters {
				t.Fatalf("AST and actual cover evidence: %+v counters %d", row, count)
			}
		})
	}
}
