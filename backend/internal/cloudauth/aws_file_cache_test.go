package cloudauth

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualAWSINISharedReadPromiseRejectionAndForcedReload(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	config := func(key string) string {
		return "[profile selected]\naws_access_key_id=" + key + "\naws_secret_access_key=PRIVATE_SECRET\n"
	}
	rows := []map[string]any{
		{"steps": []map[string]any{{"write": config("FIRST")}, {"read": true}, {"write": config("SECOND")}, {"read": true}, {"force": true}, {"read": true}}},
		{"steps": []map[string]any{{"read": true}, {"write": config("AFTER_MISSING")}, {"read": true}, {"force": true}}},
		{"steps": []map[string]any{{"write": "[__proto__]\na=b\n"}, {"read": true}, {"write": config("AFTER_PARSE_ERROR")}, {"read": true}, {"force": true}}},
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-file-cache.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_AWS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []map[string]json.RawMessage }
	if json.Unmarshal(out, &source) != nil {
		t.Fatal(string(out))
	}
	for i, row := range rows {
		home := t.TempDir()
		path := filepath.Join(home, "config")
		credentials := filepath.Join(home, "credentials")
		if err = os.WriteFile(credentials, nil, 0600); err != nil {
			t.Fatal(err)
		}
		r, err := NewAWSResolver(AWSOptions{Home: home, Environment: map[string]string{"AWS_PROFILE": "selected", "AWS_CONFIG_FILE": path, "AWS_SHARED_CREDENTIALS_FILE": credentials}, ReadFile: NativeAWSReadFile})
		if err != nil {
			t.Fatal(err)
		}
		got := []map[string]any{}
		for _, step := range row["steps"].([]map[string]any) {
			if write, present := step["write"]; present {
				if err = os.WriteFile(path, []byte(write.(string)), 0600); err != nil {
					t.Fatal(err)
				}
				continue
			}
			var value AWSCredentials
			// ResolveINI is the Source clearAwsIniCache(force) consumer. Plain fromIni
			// below uses the actual shared raw promise without force.
			if force, _ := step["force"].(bool); force {
				value, err = r.ResolveINI(ctx)
			} else {
				value, err = r.resolveProfile(ctx, "selected", "selected", r.profiles(ctx), nil, false)
			}
			if err != nil {
				got = append(got, map[string]any{"error": err.Error()})
			} else {
				got = append(got, map[string]any{"value": value})
			}
		}
		compareAWS(t, i, got, source.Rows[i]["value"])
	}
}

func TestAWSFileCachePendingReadersForceReplacementAndImmutableBytes(t *testing.T) {
	cache := &AWSFileCache{}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan []byte, 1)
	var calls atomic.Int32
	load := func(context.Context, string) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return []byte("OLD"), nil
		}
		return []byte("NEW"), nil
	}
	go func() { raw, _ := cache.Read(context.Background(), "private-config", false, load); done <- raw }()
	<-started
	forced, err := cache.Read(context.Background(), "private-config", true, load)
	if err != nil || string(forced) != "NEW" {
		t.Fatal(string(forced), err)
	}
	forced[0] = 'X'
	close(release)
	if string(<-done) != "OLD" {
		t.Fatal("old in-flight promise replaced")
	}
	cached, err := cache.Read(context.Background(), "private-config", false, load)
	if err != nil || !reflect.DeepEqual(cached, []byte("NEW")) || calls.Load() != 2 {
		t.Fatal(string(cached), err, calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = cache.Read(ctx, "cancel-private", false, func(ctx context.Context, _ string) ([]byte, error) { return nil, ctx.Err() })
	if err == nil {
		t.Fatal("cancel omitted")
	}
	_, err = cache.Read(context.Background(), "cancel-private", false, func(context.Context, string) ([]byte, error) { return []byte("RETRY"), nil })
	if err != nil {
		t.Fatal(err)
	}
}
