package skillcommands

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestExternalBundledDeclarationsGatesAndAuthoredPromptsMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	_ = os.WriteFile(filepath.Join(home, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0600)
	profiles := []map[string]string{{}, {"CC_HAHA_IMAGE_PROVIDER_KIND": "openai_images", "CC_HAHA_IMAGE_PROVIDER_ID": "own", "CC_HAHA_IMAGE_MODEL": "gpt-image-2"}, {"CC_HAHA_IMAGE_PROVIDER_KIND": "openai_images", "CC_HAHA_IMAGE_PROVIDER_ID": "own", "CC_HAHA_IMAGE_MODEL": "gpt-image-2", "CC_HAHA_IMAGE_BASE_URL": "http://127.0.0.1:1", "CC_HAHA_IMAGE_API_KEY": "fake-key"}, {"CC_HAHA_IMAGE_PROVIDER_KIND": " grok_oauth ", "CC_HAHA_IMAGE_PROVIDER_ID": " own ", "CC_HAHA_IMAGE_MODEL": " own-model "}, {"CC_HAHA_IMAGE_PROVIDER_KIND": "unknown", "CC_HAHA_IMAGE_PROVIDER_ID": "own", "CC_HAHA_IMAGE_MODEL": "model"}}
	rows := []map[string]any{}
	for _, profile := range profiles {
		calls := []map[string]string{{"name": "simplify", "args": ""}, {"name": "simplify", "args": "literal $ARGUMENTS ${Agent} $$"}, {"name": "batch", "args": ""}, {"name": "batch", "args": "\ufeff migrate owned fixtures \t"}, {"name": "update-config", "args": "[hooks-only]"}, {"name": "update-config", "args": "[hooks-only] \ufeff configure own event \t"}}
		if nativeImagegenAvailable(profile) {
			calls = append(calls, map[string]string{"name": "imagegen", "args": "private artifact diagram"})
		}
		rows = append(rows, map[string]any{"env": profile, "calls": calls})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "bundles": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Bundles []struct {
			Metadata json.RawMessage
			Prompts  []protocol.JSONObject
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Bundles) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows=%d blocked=%v %.1500s", e, len(source.Bundles), source.Capabilities.Blocked, output)
	}
	for i, profile := range profiles {
		defs, e := NativeExternalBundled(BundledOptions{Environment: profile, IsGit: func(context.Context) (bool, error) {
			_, e := os.Stat(filepath.Join(home, ".git"))
			return e == nil, nil
		}})
		if e != nil {
			t.Fatal(e)
		}
		metadata := []protocol.JSONObject{}
		for _, definition := range defs {
			metadata = append(metadata, definition.Metadata())
		}
		raw, _ := json.Marshal(metadata)
		if !bundledSameJSON(raw, source.Bundles[i].Metadata) {
			t.Errorf("metadata%d Go=%.1800s Source=%.1800s", i, raw, source.Bundles[i].Metadata)
		}
		for j, call := range rows[i]["calls"].([]map[string]string) {
			var found Definition
			for _, definition := range defs {
				if definition.Name() == call["name"] {
					found = definition
				}
			}
			argument := call["args"]
			got, e := found.Prompt(ctx, &argument, Invocation{})
			if e != nil {
				t.Fatal(e)
			}
			if got != source.Bundles[i].Prompts[j].String("text") {
				t.Errorf("prompt%d/%d differs Go=%.800s Source=%.800s", i, j, got, source.Bundles[i].Prompts[j].String("text"))
			}
		}
	}
}
func bundledSameJSON(a, b json.RawMessage) bool {
	return string(protocol.StringifyValidatedJavaScriptJSON(a)) == string(protocol.StringifyValidatedJavaScriptJSON(b))
}
