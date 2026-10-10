package securestorage

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSecureStorageFallbackAndCacheAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	step := func(action string, extra map[string]any) map[string]any {
		if extra == nil {
			extra = map[string]any{}
		}
		extra["action"] = action
		return extra
	}
	scenarios := []map[string]any{
		{"primary": `{"token":"old","future":{"keep":true}}`, "steps": []map[string]any{step("read", nil), step("read", nil), step("read", map[string]any{"advance": 31000, "failRead": true}), step("update", map[string]any{"failWrite": true, "value": map[string]any{"token": "fresh"}}), step("read", nil), step("update", map[string]any{"failRead": false, "failWrite": false, "value": map[string]any{"token": "new"}}), step("read", nil)}},
		{"primary": nil, "steps": []map[string]any{step("read", nil), step("update", map[string]any{"value": map[string]any{"token": "quote \"<&> \\", "future": true}}), step("read", nil), step("delete", nil), step("read", nil)}},
		{"primary": `{"token":"old"}`, "steps": []map[string]any{step("update", map[string]any{"value": map[string]any{"large": strings.Repeat("a", 3000)}}), step("read", nil), step("clear", nil), step("read", nil)}},
		{"primary": `{`, "steps": []map[string]any{step("read", nil), step("update", map[string]any{"failWrite": true, "value": map[string]any{"token": "fresh", "units": "😀"}}), step("read", nil), step("delete", nil)}},
	}
	now := time.UnixMilli(1700000000000)
	actual := []any{}
	for i, scenario := range scenarios {
		config := filepath.Join(home, "go", string(rune('a'+i)))
		if err := os.MkdirAll(config, 0700); err != nil {
			t.Fatal(err)
		}
		primary := scenario["primary"]
		failRead, failWrite := false, false
		calls := []string{}
		store, err := New(Options{Home: home, ConfigDir: config, Platform: "darwin", Environment: map[string]string{"USER": "owned-user"}, Now: func() time.Time { return now }, RunSecurity: func(_ context.Context, c Command) ([]byte, error) {
			switch c.Args[0] {
			case "find-generic-password":
				calls = append(calls, "read")
				if failRead {
					return nil, errors.New("private keychain unavailable")
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
				kind, payload := "write-argv", c.Args[len(c.Args)-1]
				if c.Args[0] == "-i" {
					kind = "write-stdin"
					payload = strings.TrimSuffix(strings.Split(c.Input, ` -X "`)[1], "\"\n")
				}
				calls = append(calls, kind)
				if failWrite {
					return nil, errors.New("private write failed")
				}
				data, err := hex.DecodeString(payload)
				primary = string(data)
				return nil, err
			}
			return nil, errors.New("unexpected command")
		}})
		if err != nil {
			t.Fatal(err)
		}
		observations := []any{}
		for _, s := range scenario["steps"].([]map[string]any) {
			if advance, ok := s["advance"].(int); ok {
				now = now.Add(time.Duration(advance) * time.Millisecond)
			}
			if v, ok := s["failRead"].(bool); ok {
				failRead = v
			}
			if v, ok := s["failWrite"].(bool); ok {
				failWrite = v
			}
			switch s["action"] {
			case "read":
				value, err := store.Read(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				observations = append(observations, value)
			case "update":
				data, _ := json.Marshal(s["value"])
				result := store.Update(context.Background(), record(t, string(data)))
				r := map[string]any{"success": result.Success}
				if result.Warning != "" {
					r["warning"] = result.Warning
				}
				observations = append(observations, r)
			case "delete":
				observations = append(observations, store.Delete(context.Background()))
			case "clear":
				store.Clear()
			}
		}
		var disk any
		if data, err := os.ReadFile(store.path); err == nil {
			disk = string(data)
		}
		actual = append(actual, map[string]any{"observations": observations, "calls": calls, "primary": primary, "disk": disk})
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-secure-storage.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_SECURE_STORAGE_FIXTURE=1", "NODE_ENV=production", "USER=owned-user", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(scenarios)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	expected, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source fixture: %v %s", err, stderr.String())
	}
	data, _ := json.Marshal(actual)
	var a, b any
	_ = json.Unmarshal(data, &a)
	if err := json.Unmarshal(expected, &b); err != nil {
		t.Fatalf("Source JSON: %v %s", err, expected)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("secure storage differs: Go=%s Source=%s", data, expected)
	}
}
