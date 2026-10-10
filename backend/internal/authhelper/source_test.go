package authhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestHelperCacheAndTTLsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	ttls := []string{"", "0", "10", "+10", "-0", "-1", "1e3", " 23tail", "0xFF", "-0xFF", "NaN", "Infinity", "  ", "\ufeff12", "9999999999999999999999999", "999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999"}
	rows := []map[string]any{
		{"responses": []map[string]any{{"value": "first"}, {"value": "fresh"}, {"value": "after-clear"}}, "actions": []map[string]any{{}, {"now": 1009}, {"now": 1010}, {"clear": true}}},
		{"responses": []map[string]any{{"value": "first"}, {"error": "temporary"}, {"value": "later"}}, "actions": []map[string]any{{}, {"now": 1010}, {"now": 1011}, {"now": 1020}}},
		{"responses": []map[string]any{{"error": "cold"}, {"error": "stale"}, {"value": "recovered"}}, "actions": []map[string]any{{}, {"now": 1009}, {"now": 1010}, {"clear": true}}},
	}
	gotTTL := []any{}
	for _, value := range ttls {
		number := TTL(value)
		if math.IsInf(number, 0) {
			gotTTL = append(gotTTL, nil)
		} else {
			gotTTL = append(gotTTL, number)
		}
	}
	gotRows := []map[string]any{}
	for _, row := range rows {
		var mu sync.Mutex
		now := float64(1000)
		calls := 0
		logs := []string{}
		cache := New(Options{NowMillis: func() float64 { mu.Lock(); defer mu.Unlock(); return now }, Error: func(s string) { logs = append(logs, "apiKeyHelper failed: "+s) }})
		responses := row["responses"].([]map[string]any)
		r := Request{Command: "actual-private-command", TTL: "10", NonInteractive: true, Execute: func(context.Context, string) (string, error) {
			value := responses[calls]
			calls++
			if failure, ok := value["error"].(string); ok {
				return "", errors.New(failure)
			}
			return value["value"].(string), nil
		}}
		steps := []map[string]any{}
		for _, action := range row["actions"].([]map[string]any) {
			if value, ok := action["now"].(int); ok {
				mu.Lock()
				now = float64(value)
				mu.Unlock()
			}
			if action["clear"] == true {
				cache.Clear()
			}
			value, err := cache.Get(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			waitCache(t, func() bool { cache.mu.Lock(); defer cache.mu.Unlock(); return cache.inflight == nil })
			steps = append(steps, map[string]any{"value": value, "cached": cache.Cached(), "calls": calls})
		}
		gotRows = append(gotRows, map[string]any{"steps": steps, "errors": logs})
		_ = cache.Close(context.Background())
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-auth-helper-cache.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTH_HELPER_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"ttls": ttls, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	got, _ := json.Marshal(map[string]any{"ttl": gotTTL, "rows": gotRows})
	var actual, expected any
	_ = json.Unmarshal(got, &actual)
	_ = json.Unmarshal(output, &expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("actual helper Source mismatch\ngot%s\nwant%s", got, output)
	}
}
