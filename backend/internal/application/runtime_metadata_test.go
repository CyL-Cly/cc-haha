package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopRuntimeMetadataSurvivesClosingBeforeFirstTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	path := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(o.Native.CWD), o.Arguments.SessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	old := raw(map[string]any{"type": "session-meta", "isMeta": true, "workDir": o.WorkDir, "future": map[string]any{"keep": 1}})
	old = append(old, '\n')
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "runtimeProviderId", Value: json.RawMessage(`"selected-before-first-turn"`)}, protocol.JSONField{Name: "runtimeModelId", Value: json.RawMessage(`"fixture-model"`)})
	if err := s.process.AppendSessionMetadata(ctx, metadata); err != nil {
		t.Fatal(err)
	}
	confirmed, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(confirmed, []byte(`"runtimeProviderId":"selected-before-first-turn"`)) {
		t.Fatalf("runtime metadata acknowledged before the physical append: %s %v", confirmed, err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, old) || !bytes.Contains(data, []byte(`"runtimeProviderId":"selected-before-first-turn"`)) {
		t.Fatalf("desktop acknowledged metadata but lost it on close before a turn: %s", data)
	}
}

func TestRuntimeMetadataDoesNotCreateMissingOrDisabledTranscripts(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "missing"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			path := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(o.Native.CWD), o.Arguments.SessionID+".jsonl")
			var old []byte
			if disabled {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				old = append(raw(map[string]any{"type": "session-meta", "isMeta": true, "workDir": o.WorkDir}), '\n')
				if err := os.WriteFile(path, old, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"cleanupPeriodDays":0}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close(context.Background()) })
			metadata := protocol.NewJSONObject(protocol.JSONField{Name: "runtimeModelId", Value: json.RawMessage(`"private-selection"`)})
			if err := s.process.AppendSessionMetadata(ctx, metadata); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if disabled {
				if err != nil || !bytes.Equal(data, old) {
					t.Fatalf("disabled persistence changed a transcript: %s %v", data, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("runtime metadata created an absent session: %s %v", data, err)
			}
		})
	}
}
