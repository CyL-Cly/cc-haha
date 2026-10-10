package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeImageCatalogEnabledStateMatchesActualSourceAcrossRuntimeChanges(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	for _, kind := range []string{"", "other", "openai_oauth", "grok_oauth", "openai_images", " \ufeffopenai_images\u00a0"} {
		for _, complete := range []bool{false, true} {
			env := map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": kind, "CC_HAHA_IMAGE_PROVIDER_ID": "provider", "CC_HAHA_IMAGE_MODEL": "model"}
			if complete {
				env["CC_HAHA_IMAGE_BASE_URL"], env["CC_HAHA_IMAGE_API_KEY"] = "https://private.invalid", "fake-key"
			}
			for _, name := range []string{"ImageGen", "ImageEdit"} {
				rows = append(rows, map[string]any{"name": name, "env": env})
			}
		}
	}
	for _, field := range []string{"CC_HAHA_IMAGE_PROVIDER_ID", "CC_HAHA_IMAGE_MODEL"} {
		rows = append(rows, map[string]any{"name": "ImageGen", "env": map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "openai_oauth", "CC_HAHA_IMAGE_PROVIDER_ID": "provider", "CC_HAHA_IMAGE_MODEL": "model", field: " \ufeff\u00a0"}})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-imagegen-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGEGEN_TOOL_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	encoded, _ := json.Marshal(rows)
	command.Stdin = bytes.NewReader(encoded)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		t.Fatal("Source image enabled", err, stderr.String())
	}
	var expected []struct{ Enabled bool }
	if err = json.Unmarshal(data, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal("Source enabled output", err, string(data))
	}
	m := openAIAuthMainFixture(t)
	err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for index, row := range rows {
			env := row["env"].(map[string]string)
			for _, key := range []string{"CC_HAHA_IMAGE_PROVIDER_KIND", "CC_HAHA_IMAGE_PROVIDER_ID", "CC_HAHA_IMAGE_MODEL", "CC_HAHA_IMAGE_BASE_URL", "CC_HAHA_IMAGE_API_KEY"} {
				if err := m.environment.SetProperty(quoteMain(key), quoteMain(env[key])); err != nil {
					return err
				}
			}
			enabled, err := m.nativeTools.ImageToolsEnabled(scope)
			if err != nil || enabled != expected[index].Enabled {
				return fmt.Errorf("row%d native enabled=%v Source=%v error=%v", index, enabled, expected[index].Enabled, err)
			}
			_, tools, err := m.nativeTools.Catalog(scope)
			if err != nil {
				return err
			}
			found := false
			for _, tool := range tools {
				found = found || tool.Name == row["name"].(string)
			}
			if found != enabled {
				return fmt.Errorf("row%d native actual registry=%v enabled=%v", index, found, enabled)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
