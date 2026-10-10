package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestPluginUpdateSharesSettingsTransactionAndPreservesUnknownState(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(config, "settings.json")
	seed := []byte(`{"future":{"keep":"\ud800"},"enabledPlugins":{"unknown@market":{"future":true},"disabled@market":false}}`)
	if err := os.WriteFile(file, seed, 0600); err != nil {
		t.Fatal(err)
	}
	o := Options{Store: storage.New(storage.Options{}), HomeDir: root, ConfigDir: config, ProjectRoot: root, Environment: func() map[string]string { return map[string]string{} }, EnsureMigrated: func(context.Context, string) error { return nil }, GitExecutable: "git", ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	ctx := context.Background()
	var wg sync.WaitGroup
	for _, id := range []string{"a@market", "b@market"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := s.UpdateEnabledPlugin(ctx, id, &yes); err != nil {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	if err := s.UpdateEnabledPlugin(ctx, "a@market", &no); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateEnabledPlugin(ctx, "b@market", nil); err != nil {
		t.Fatal(err)
	}
	value, err := s.GetUserSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var enabled storage.Object
	if err := json.Unmarshal(value["enabledPlugins"], &enabled); err != nil {
		t.Fatal(err)
	}
	if string(enabled["a@market"]) != "false" || enabled["b@market"] != nil || enabled["unknown@market"] == nil || string(enabled["disabled@market"]) != "false" {
		t.Fatalf("plugin settings lost a key or changed false into deletion: %s", value["enabledPlugins"])
	}
	var future storage.Object
	if err := json.Unmarshal(value["future"], &future); err != nil {
		t.Fatal(err)
	}
	if string(future["keep"]) != `"\ud800"` {
		t.Fatalf("unknown settings or UTF-16 changed: %s", value["future"])
	}
}
