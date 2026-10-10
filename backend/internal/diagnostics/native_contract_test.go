package diagnostics

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The sealed original Bun logger and four timing fault programs use the same
// native Date.toISOString. Its RangeError message is "Invalid Date"; the old
// Go expectation was copied from another JavaScript runtime.
func TestNativeBunInvalidDateContract(t *testing.T) {
	_, first := isoDate(time.UnixMilli(8640000000000001))
	_, second := isoDate(time.UnixMilli(-8640000000000001))
	var typed *InvalidDateError
	if !errors.As(first, &typed) || typed.ErrorName() != "RangeError" || typed.Error() != "Invalid Date" || first == second {
		t.Fatalf("original Bun invalid Date type/message/fresh identity: %T %v, second=%v", first, first, second)
	}
}

func TestNativeBunRecursiveMkdirExistingLeafContract(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "existing-file")
	if err := os.WriteFile(leaf, []byte("owned original non-directory input\n"), 0666); err != nil {
		t.Fatal(err)
	}
	files := OSFileSystem{}
	if err := files.MkdirAll(leaf, 0700); err != nil {
		t.Fatalf("Bun recursive EEXIST swallowed before actual lstat: %v", err)
	}
	info, err := files.Lstat(leaf)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("actual regular leaf lost", info, err)
	}
	// An intermediate file is a different native boundary, not an existing leaf.
	if err := files.MkdirAll(filepath.Join(leaf, "missing-child"), 0700); err == nil {
		t.Fatal("non-directory parent native error swallowed")
	}
}
