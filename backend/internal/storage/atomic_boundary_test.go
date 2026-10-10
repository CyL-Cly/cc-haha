package storage

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type atomicOrderFS struct {
	osFileSystem
	events *[]string
	denied error
}

func (boundary atomicOrderFS) MkdirAll(path string, mode fs.FileMode) error {
	*boundary.events = append(*boundary.events, "mkdir")
	if boundary.denied != nil {
		return boundary.denied
	}
	return boundary.osFileSystem.MkdirAll(path, mode)
}

func (boundary atomicOrderFS) Remove(path string) error {
	*boundary.events = append(*boundary.events, "unlink")
	return boundary.osFileSystem.Remove(path)
}

type atomicOrderRandom struct{ events *[]string }

func (random atomicOrderRandom) Read(data []byte) (int, error) {
	*random.events = append(*random.events, "random")
	for index := range data {
		data[index] = byte(index)
	}
	return len(data), nil
}

func TestAtomicTemporaryNameConsumesClockBeforeRandomAndMkdir(t *testing.T) {
	events := []string{}
	store := New(Options{FS: atomicOrderFS{events: &events}, Now: func() time.Time {
		events = append(events, "clock")
		return time.UnixMilli(1234)
	}, Random: atomicOrderRandom{&events}})
	if err := store.AtomicWriteJSON(filepath.Join(t.TempDir(), "settings.json"), "settings", json.RawMessage(`{}`), WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"clock", "random", "mkdir"}) {
		t.Fatalf("source temporary-name evaluation order changed: %v", events)
	}
}

func TestManagedMkdirOutsideCatchKeepsCauseAndNeverUnlinks(t *testing.T) {
	events := []string{}
	denied := errors.New("fixture mkdir denied")
	store := New(Options{FS: atomicOrderFS{events: &events, denied: denied}, Now: func() time.Time {
		events = append(events, "clock")
		return time.UnixMilli(1234)
	}, Random: atomicOrderRandom{&events}})
	err := store.AtomicWriteJSON(filepath.Join(t.TempDir(), "settings.json"), "settings", json.RawMessage(`{}`), WriteOptions{MkdirFailureOutsideCatch: true})
	if err != denied || !reflect.DeepEqual(events, []string{"clock", "random", "mkdir"}) {
		t.Fatalf("managed mkdir catch/cleanup/evaluation order changed: %v, %v", err, events)
	}
}
