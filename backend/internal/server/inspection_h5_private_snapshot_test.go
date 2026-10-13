package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This opt-in receipt replays a maintainer-created, credential-free frozen
// private snapshot. The permanent finite regression follows from its cause.
func TestFrozenPrivateH5ColdInspectionSnapshot(t *testing.T) {
	historyPath, configPath := os.Getenv("CC_HAHA_H5_BEFORE_HISTORY"), os.Getenv("CC_HAHA_H5_BEFORE_CONFIG")
	if historyPath == "" || configPath == "" {
		t.Skip("explicit credential-free frozen snapshot required")
	}
	history, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var frozen map[string]json.RawMessage
	if err = json.Unmarshal(config, &frozen); err != nil {
		t.Fatal(err)
	}
	s, info := inspectionCacheServer(t)
	if err = os.WriteFile(info.Path, history, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(s.options.ConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.options.ConfigDir, "providers.json"), frozen["providers"], 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(s.options.HomeDir, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.options.HomeDir, ".claude", "settings.json"), frozen["settings"], 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.getInspectionTranscript(context.Background(), info)
	if err != nil {
		t.Fatalf("actual frozen private cold snapshot error: %T: %v", err, err)
	}
	if snapshot == nil || len(snapshot.ContextEstimate) == 0 || string(snapshot.ContextEstimate) == "null" {
		t.Fatal("cold footer missing estimate")
	}
	after, err := os.ReadFile(info.Path)
	if err != nil || !bytes.Equal(history, after) {
		t.Fatal("inspection changed frozen transcript", err)
	}
}
