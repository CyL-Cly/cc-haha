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

func TestActualSourceCloudMemoColdDedupClearAndOrphanStaleCannotClobber(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cloud-helper-cache.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLOUD_HELPER_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewBufferString(`{"rows":[]}`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Concurrent []any }
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	for i, profile := range []string{"cold-clear", "stale-clear", "stale-error-clear"} {
		var mu sync.Mutex
		now := float64(1000)
		calls := 0
		errorsOut := []string{}
		type response struct {
			value string
			err   error
		}
		started := make(chan chan response, 3)
		cache := NewCloudCache(Options{NowMillis: func() float64 { mu.Lock(); defer mu.Unlock(); return now }, Error: func(text string) { errorsOut = append(errorsOut, text) }})
		execute := func(active context.Context) (json.RawMessage, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			ready := make(chan response, 1)
			started <- ready
			select {
			case value := <-ready:
				data, _ := json.Marshal(value.value)
				return data, value.err
			case <-active.Done():
				return nil, active.Err()
			}
		}
		first, err := cache.Begin(execute)
		if err != nil {
			t.Fatal(err)
		}
		slot := <-started
		old := first
		var duplicate *CloudLookup
		if profile == "cold-clear" {
			duplicate, err = cache.Begin(execute)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			slot <- response{value: "initial"}
			if _, err = first.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			now = 3601001
			mu.Unlock()
			old, err = cache.Begin(execute)
			if err != nil {
				t.Fatal(err)
			}
			slot = <-started
		}
		cache.Clear()
		fresh, err := cache.Begin(execute)
		if err != nil {
			t.Fatal(err)
		}
		newSlot := <-started
		newSlot <- response{value: "fresh"}
		freshValue, err := fresh.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if profile == "stale-error-clear" {
			slot <- response{err: errors.New("orphan stale failure")}
		} else {
			slot <- response{value: "older"}
		}
		oldValue, err := old.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var duplicateValue json.RawMessage
		if duplicate != nil {
			duplicateValue, err = duplicate.Wait(ctx)
			if err != nil {
				t.Fatal(err)
			}
		}
		cache.jobs.Wait()
		final, err := cache.Begin(execute)
		if err != nil {
			t.Fatal(err)
		}
		cached, err := final.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		decode := func(raw json.RawMessage) any { var value any; _ = json.Unmarshal(raw, &value); return value }
		data, _ := json.Marshal(map[string]any{"profile": profile, "freshResult": decode(freshValue), "oldResult": decode(oldValue), "duplicateResult": decode(duplicateValue), "cached": decode(cached), "calls": calls, "errors": errorsOut})
		var got any
		_ = json.Unmarshal(data, &got)
		if !reflect.DeepEqual(got, source.Concurrent[i]) {
			t.Errorf("case%d Go%s Source%v", i, data, source.Concurrent[i])
		}
		if err = cache.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
