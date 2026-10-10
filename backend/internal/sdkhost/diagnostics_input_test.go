package sdkhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
)

func inputDiagnostics(t *testing.T, badDate bool) (*diagnostics.Process, string, *int) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "diagnostic", "events")
	entries := new(int)
	process, err := diagnostics.NewProcess(diagnostics.Options{Environment: func(string) string { return base }, Now: func() time.Time {
		*entries++
		if badDate {
			return time.UnixMilli(8640000000000001)
		}
		return time.UnixMilli(1700000000000)
	}, NowMillis: func() int64 { return 1700000000000 }, PID: 123, Platform: "darwin", Files: diagnostics.OSFileSystem{}})
	if err != nil {
		t.Fatal(err)
	}
	return process, base + ".123.current.jsonl", entries
}

func TestDiagnosticsStructuredInputNewlineOnlyAndOriginalEOFBehavior(t *testing.T) {
	process, path, entries := inputDiagnostics(t, false)
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		raw, err := chunkIterator(owner, []string{"{\"type\":\"keep_alive\"}\n{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"private first\"}}\n", "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"private last\"}}"})
		if err != nil {
			return err
		}
		input, err := NewPlainStructuredInputWithDiagnostics(owner, raw, process)
		if err != nil {
			return err
		}
		for i := 0; i < 3; i++ {
			next, err := input.Next(scope)
			if err != nil {
				return err
			}
			result, err := next.Await(scope)
			if err != nil {
				return err
			}
			if result.Done != (i == 2) {
				return fmt.Errorf("EOF/input ordering changed: %v", result)
			}
			if i != 0 && *entries != 1 {
				return fmt.Errorf("EOF or keepalive added parsed log: %d", *entries)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || bytes.Count(raw, []byte{'\n'}) != 1 || !bytes.Contains(raw, []byte(`"data":{"type":"user"}`)) || bytes.Contains(raw, []byte("private")) {
		t.Fatalf("stdin diagnostics: %s/%v", raw, err)
	}
}

func TestDiagnosticsStructuredInputParseAndLogFailurePrecedence(t *testing.T) {
	for _, item := range []struct {
		name, chunk string
		wantClocks  int
	}{
		{"malformed-first", "{broken\n", 0},
		{"newline-log-fails", "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"private\"}}\n", 1},
	} {
		t.Run(item.name, func(t *testing.T) {
			process, path, entries := inputDiagnostics(t, true)
			owner := cooperative.New()
			defer owner.Close()
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				raw, err := chunkIterator(owner, []string{item.chunk})
				if err != nil {
					return err
				}
				input, err := NewPlainStructuredInputWithDiagnostics(owner, raw, process)
				if err != nil {
					return err
				}
				next, err := input.Next(scope)
				if err != nil {
					return err
				}
				result, err := next.Await(scope)
				if err == nil || result.Defined {
					return fmt.Errorf("failure yielded input: %v/%v", result, err)
				}
				return nil
			})
			if err != nil || *entries != item.wantClocks {
				t.Fatalf("first failure: %v/%d", err, *entries)
			}
			if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed parse/log touched native log")
			}
		})
	}
}

func TestDiagnosticsRunStreamRejectsSinkOrDateBeforeNativeInputNext(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "started-error", true: "different-process"}[mismatch], func(t *testing.T) {
			process, _, entries := inputDiagnostics(t, true)
			owner := cooperative.New()
			defer owner.Close()
			readCount := 0
			raw, err := NewOwnerIterator(owner, func(scope *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
				readCount++
				return iterator.Complete(scope)
			})
			if err != nil {
				t.Fatal(err)
			}
			inputSink := process
			if mismatch {
				inputSink = nil
			}
			input, err := NewPlainStructuredInputWithDiagnostics(owner, raw, inputSink)
			if err != nil {
				t.Fatal(err)
			}
			host := &Host{options: Options{Owner: owner, Diagnostics: process}}
			err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if err := host.RunStream(scope, context.Background(), input, StreamOptions{}); err == nil {
					return errors.New("before-next failure was swallowed")
				}
				return nil
			})
			wantEntries := 1
			if mismatch {
				wantEntries = 0
			}
			if err != nil || readCount != 0 || *entries != wantEntries {
				t.Fatalf("failure started second input consumer: %v/%d/%d", err, readCount, *entries)
			}
		})
	}
}

func TestDiagnosticsOriginalEnvironmentKeepsPrivateRegistryAndExcludesRequest(t *testing.T) {
	original := map[string]string{"CLAUDE_CODE_SIMPLE": "1", "ANTHROPIC_API_KEY": "offline-fake", "CLAUDE_CODE_DIAGNOSTICS_FILE": "/private/original/events"}
	for _, neutral := range []bool{false, true} {
		view, err := requestEnvironmentForProfile(original, neutral)
		if err != nil || len(view) != 2 || view["ANTHROPIC_API_KEY"] != "offline-fake" || original["CLAUDE_CODE_DIAGNOSTICS_FILE"] != "/private/original/events" {
			t.Fatalf("private registry/request separation: %v/%v", view, err)
		}
		if _, exists := view["CLAUDE_CODE_DIAGNOSTICS_FILE"]; exists {
			t.Fatal("diagnostic path entered SDK request options")
		}
	}
}
