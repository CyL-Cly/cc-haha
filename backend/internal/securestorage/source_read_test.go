package securestorage

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRawSecureStoreOldRootsAndSharedCacheMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	for _, platform := range []string{"linux", "darwin"} {
		for _, raw := range []any{nil, "null", "false", "0", `"legacy"`, `[]`, `[1,null]`, `{"future":{"keep":true},"openaiCodexOauth":false}`, "\ufeff {\"token\":\"bom\"} \n", "invalid"} {
			row := map[string]any{"platform": platform, "steps": []map[string]any{{"action": "sync"}, {"action": "async"}, {"action": "async", "advance": 31000, "failRead": true}, {"action": "update", "value": map[string]any{"future": "new"}, "failWrite": true}, {"action": "async"}, {"action": "update", "value": []any{1, "old-shape"}, "failRead": false, "failWrite": false}, {"action": "sync"}, {"action": "delete"}, {"action": "async"}}}
			if platform == "darwin" {
				row["primary"] = raw
				row["secondary"] = `{"fallback":"keep"}`
			} else if raw != nil {
				row["secondary"] = raw
			}
			rows = append(rows, row)
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-secure-storage-raw.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SECURE_RAW_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "USER=owned-user"}
	payload, _ := json.Marshal(rows)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source raw: %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape: %s", data)
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, ".claude")
			if err := os.Mkdir(config, 0700); err != nil {
				t.Fatal(err)
			}
			primary := row["primary"]
			if secondary, ok := row["secondary"]; ok {
				if err := os.WriteFile(filepath.Join(config, ".credentials.json"), []byte(secondary.(string)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			now := time.UnixMilli(1700000000000)
			failRead, failWrite := false, false
			calls := []string{}
			store, err := New(Options{Home: root, ConfigDir: config, Platform: row["platform"].(string), Environment: map[string]string{"USER": "owned-user"}, Now: func() time.Time { return now }, RunSecurity: func(_ context.Context, c Command) ([]byte, error) {
				switch c.Args[0] {
				case "find-generic-password":
					calls = append(calls, "read")
					if failRead {
						return nil, errors.New("private failure")
					}
					if primary == nil {
						return nil, nil
					}
					return []byte(primary.(string)), nil
				case "delete-generic-password":
					calls = append(calls, "delete")
					primary = nil
					return nil, nil
				case "-i", "add-generic-password":
					calls = append(calls, "write")
					if failWrite {
						return nil, errors.New("private failure")
					}
					value := c.Args[len(c.Args)-1]
					if c.Args[0] == "-i" {
						value = strings.TrimSuffix(strings.Split(c.Input, ` -X "`)[1], "\"\n")
					}
					raw, e := hex.DecodeString(value)
					primary = string(raw)
					return nil, e
				}
				return nil, errors.New("unexpected private command")
			}})
			if err != nil {
				t.Fatal(err)
			}
			values := []any{}
			for _, step := range row["steps"].([]map[string]any) {
				if advance, ok := step["advance"].(int); ok {
					now = now.Add(time.Duration(advance) * time.Millisecond)
				}
				if fail, ok := step["failRead"].(bool); ok {
					failRead = fail
				}
				if fail, ok := step["failWrite"].(bool); ok {
					failWrite = fail
				}
				switch step["action"] {
				case "sync":
					raw, e := store.ReadSourceJSON(ctx)
					if e != nil {
						t.Fatal(e)
					}
					values = append(values, raw)
				case "async":
					raw, e := store.ReadSourceJSONAsync(ctx)
					if e != nil {
						t.Fatal(e)
					}
					values = append(values, raw)
				case "update":
					raw, _ := json.Marshal(step["value"])
					result := store.UpdateSourceJSON(ctx, raw)
					r := map[string]any{"success": result.Success}
					if result.Warning != "" {
						r["warning"] = result.Warning
					}
					values = append(values, r)
				case "delete":
					values = append(values, store.Delete(ctx))
				case "clear":
					store.Clear()
				}
			}
			var disk any
			if raw, e := os.ReadFile(store.path); e == nil {
				disk = string(raw)
			}
			actual := map[string]any{"values": values, "calls": calls, "primary": primary, "disk": disk}
			raw, _ := json.Marshal(actual)
			var got, want any
			_ = json.Unmarshal(raw, &got)
			_ = json.Unmarshal(expected[i], &want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Go%s\nSource%s", raw, expected[i])
			}
		})
	}
}

func TestRawSecureAsyncReadDeduplicatesAndCannotOverwriteSharedUpdate(t *testing.T) {
	home := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	var reads atomic.Int32
	store, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "darwin", Environment: map[string]string{"USER": "private"}, RunSecurity: func(_ context.Context, c Command) ([]byte, error) {
		if c.Args[0] == "find-generic-password" {
			if reads.Add(1) == 1 {
				close(entered)
				<-release
			}
			return []byte(`{"version":"old"}`), nil
		}
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan json.RawMessage, 2)
	for i := 0; i < 2; i++ {
		go func() {
			value, e := store.ReadSourceJSONAsync(context.Background())
			if e != nil {
				t.Error(e)
			}
			results <- value
		}()
	}
	<-entered
	// Sync update invalidates this flight before writing its authoritative cache.
	if result := store.UpdateSourceJSON(context.Background(), json.RawMessage(`{"version":"new","future":true}`)); !result.Success {
		t.Fatal(result)
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-results:
		case <-time.After(3 * time.Second):
			t.Fatal("async read did not join")
		}
	}
	value, err := store.ReadSourceJSON(context.Background())
	if err != nil || string(value) != `{"version":"new","future":true}` {
		t.Fatalf("stale flight overwrote shared cache: %s %v", value, err)
	}
	// One physical async flight plus the original update's synchronous before read.
	if reads.Load() > 2 {
		t.Fatal("duplicate async subprocess", reads.Load())
	}
}
