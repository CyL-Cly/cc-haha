package authhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestActualSourceCloudMemoCachesNullStrictTTLDeletesStaleFailureAndClears(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	profiles := []map[string]any{
		{"responses": []map[string]any{{"value": nil}, {"value": map[string]string{"accessKeyId": "actual-private"}}, {"value": false}}, "actions": []map[string]any{{}, {"now": 3601000}, {"now": 3601001}, {}, {"clear": true}}},
		{"responses": []map[string]any{{"value": false}, {"error": "stale failure"}, {"value": true}}, "actions": []map[string]any{{}, {"now": 3601001}, {}}},
		{"responses": []map[string]any{{"error": "cold failure"}, {"value": false}, {"value": true}}, "actions": []map[string]any{{}, {}, {"clear": true}}},
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cloud-helper-cache.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLOUD_HELPER_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"rows": profiles})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []any }
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	for i, profile := range profiles {
		var mu sync.Mutex
		now := float64(1000)
		calls := 0
		errorsOut := []string{}
		cache := NewCloudCache(Options{NowMillis: func() float64 { mu.Lock(); defer mu.Unlock(); return now }, Error: func(text string) { errorsOut = append(errorsOut, text) }})
		execute := func(context.Context) (json.RawMessage, error) {
			mu.Lock()
			defer mu.Unlock()
			row := profile["responses"].([]map[string]any)[calls]
			calls++
			if value, ok := row["error"].(string); ok {
				return nil, errors.New(value)
			}
			data, _ := json.Marshal(row["value"])
			return data, nil
		}
		steps := []map[string]any{}
		for _, action := range profile["actions"].([]map[string]any) {
			if value, ok := action["now"].(int); ok {
				mu.Lock()
				now = float64(value)
				mu.Unlock()
			}
			if action["clear"] == true {
				cache.Clear()
			}
			lookup, err := cache.Begin(execute)
			if err != nil {
				t.Fatal(err)
			}
			value, err := lookup.Wait(ctx)
			cache.jobs.Wait()
			var errorText any
			if err != nil {
				errorText = err.Error()
			}
			var decoded any
			if value != nil {
				_ = json.Unmarshal(value, &decoded)
			}
			steps = append(steps, map[string]any{"value": decoded, "error": errorText, "calls": calls})
		}
		data, _ := json.Marshal(map[string]any{"steps": steps, "errors": errorsOut})
		var got any
		_ = json.Unmarshal(data, &got)
		if !reflect.DeepEqual(got, source.Rows[i]) {
			t.Errorf("case%d Go%s Source%v", i, data, source.Rows[i])
		}
		if err = cache.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
