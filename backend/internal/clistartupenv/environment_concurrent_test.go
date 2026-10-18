package clistartupenv

import (
	"fmt"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestEnvironmentAssignIsOneSynchronousSnapshotForNativeReaders(t *testing.T) {
	pair := func(value string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "first", Value: quote(value)}, protocol.JSONField{Name: "second", Value: quote(value)})
	}
	environment, err := NewEnvironment(pair("a"))
	if err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 1000 {
				snapshot := environment.Snapshot()
				if snapshot.String("first") != snapshot.String("second") {
					failures <- fmt.Errorf("partial Object.assign exposed: %q / %q", snapshot.String("first"), snapshot.String("second"))
					return
				}
			}
		}()
	}
	for range 1000 {
		if err := environment.Assign(pair("b")); err != nil {
			t.Fatal(err)
		}
		if err := environment.Assign(pair("a")); err != nil {
			t.Fatal(err)
		}
	}
	readers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
