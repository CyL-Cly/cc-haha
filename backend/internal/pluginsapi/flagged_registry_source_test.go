package pluginsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestFlaggedRegistryActualSourceParsingExpiryAtomicWriteAndFailureCacheTiming(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source required")
	}
	initials := []string{`{"plugins":{"expired":{"flaggedAt":"2026-10-01T00:00:00Z","seenAt":"2026-10-01T00:00:00Z"},"unseen":{"flaggedAt":"2026-10-01T00:00:00Z","future":true},"invalid-date":{"flaggedAt":"raw","seenAt":"not-a-date"},"bad":7}}`, `{"plugins":[{"flaggedAt":"first"},{"flaggedAt":false},{"flaggedAt":"third","seenAt":""}]}`, `{"plugins":{"\u005f_proto__":{"flaggedAt":"prototype"},"\ud800":{"flaggedAt":"\udfff"},"unseen":{"flaggedAt":"good"}}}`, `invalid JSON`}
	steps := []protocol.JSONObject{obj([]byte(`{"op":"snapshot"}`)), obj([]byte(`{"op":"load"}`)), obj([]byte(`{"op":"seen","ids":["\ud800"]}`)), obj([]byte(`{"op":"remove","id":"\ud800"}`)), obj([]byte(`{"op":"seen","ids":["unseen","missing"]}`)), obj([]byte(`{"op":"advance","ms":1000}`)), obj([]byte(`{"op":"seen","ids":["unseen"]}`)), obj([]byte(`{"op":"add","id":"fresh@mkt"}`)), obj([]byte(`{"op":"seen","ids":["fresh@mkt"]}`)), obj([]byte(`{"op":"block"}`)), obj([]byte(`{"op":"add","id":"failed@mkt"}`)), obj([]byte(`{"op":"seen","ids":["invalid-date"]}`)), obj([]byte(`{"op":"remove","id":"fresh@mkt"}`)), obj([]byte(`{"op":"unblock"}`)), obj([]byte(`{"op":"advance","ms":172800000}`)), obj([]byte(`{"op":"load"}`)), obj([]byte(`{"op":"remove","id":"missing"}`))}
	for index, initial := range initials {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-flagged-registry.fixture.ts")
			now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_FLAGGED_REGISTRY_SOURCE=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "initial": initial, "steps": steps, "now": now.UnixMilli()}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual Source flagged registry %v %.6000s", err, output)
			}
			var original []protocol.JSONObject
			if err = json.Unmarshal(output, &original); err != nil {
				t.Fatal(err, string(output))
			}
			if len(original) != len(steps) {
				t.Fatal("missing Source step", string(output))
			}
			directory := filepath.Join(t.TempDir(), "plugins")
			_ = os.MkdirAll(directory, 0700)
			target := filepath.Join(directory, "flagged-plugins.json")
			_ = os.WriteFile(target, []byte(initial), 0644)
			registry := NewFlaggedRegistry(directory)
			saved := []byte{}
			for i, step := range steps {
				switch step.String("op") {
				case "load":
					err = registry.Load(context.Background(), now)
				case "add":
					err = registry.Add(context.Background(), step.String("id"), now)
				case "seen":
					var ids []json.RawMessage
					_ = json.Unmarshal(step.Get("ids"), &ids)
					err = registry.MarkSeenJSON(context.Background(), ids, now)
				case "remove":
					err = registry.RemoveJSON(context.Background(), step.Get("id"))
				case "advance":
					var ms int64
					_ = json.Unmarshal(step.Get("ms"), &ms)
					now = now.Add(time.Duration(ms) * time.Millisecond)
				case "block":
					saved, _ = os.ReadFile(target)
					_ = os.Remove(target)
					_ = os.Mkdir(target, 0700)
					_ = os.WriteFile(filepath.Join(target, "physical-blocker"), []byte("owned failed rename"), 0600)
				case "unblock":
					_ = os.RemoveAll(target)
					_ = os.WriteFile(target, saved, 0644)
				}
				if err != nil {
					t.Fatal("Source catches physical write failures", step.String("op"), err)
				}
				value := rec(field("cache", registry.Snapshot()), field("disk", nil), field("mode", nil), field("temporaryCount", 0))
				if stat, e := os.Stat(target); e == nil && stat.Mode().IsRegular() {
					data, _ := os.ReadFile(target)
					if json.Valid(data) {
						value.Set("disk", data)
					}
					value.Set("mode", raw(stat.Mode().Perm()))
				}
				entries, _ := os.ReadDir(directory)
				count := 0
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".tmp") {
						count++
					}
				}
				value.Set("temporaryCount", raw(count))
				actual, e := protocol.ParseAndStringifyJavaScriptJSON(raw(value))
				if e != nil {
					t.Fatal(e)
				}
				want, e := protocol.ParseAndStringifyJavaScriptJSON(raw(original[i]))
				if e != nil {
					t.Fatal(e)
				}
				if string(actual) != string(want) {
					t.Fatalf("Source registry step%d %s actual%s Source%s", i, step.String("op"), actual, want)
				}
			}
		})
	}
}
