package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRawAtomicJSONPreservesOwnedBytesAndRejectsInvalidInputBeforeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "registry.json")
	store := New(Options{})
	value := json.RawMessage("{\n  \"version\": 2,\n  \"escaped\": \"\\ud800\"\n}")
	if err := store.AtomicWriteRawJSON(path, "registry", value, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(value) {
		t.Fatalf("bytes changed: %q %v", got, err)
	}
	if err := store.AtomicWriteRawJSON(path, "registry", json.RawMessage(`{"invalid":`), WriteOptions{}); err == nil {
		t.Fatal("invalid JSON replaced the committed registry")
	}
	got, _ = os.ReadFile(path)
	if string(got) != string(value) {
		t.Fatal("invalid write damaged committed state")
	}
}
