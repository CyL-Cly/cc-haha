package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func windowsLifecycleFixture(t *testing.T, call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) (*WindowsTools, *Service) {
	t.Helper()
	o := fixtureOptions(t)
	o.Platform = "win32"
	s := fixtureService(t, o)
	if err := s.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	p, err := NewWindowsTools(WindowsToolsOptions{Native: NativeToolsOptions{Service: s, SessionID: "actual-windows-sid", NativeCall: call}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return p, s
}
func TestWindowsPixelCancelCloseJoinHeldMouseAndNoLaterDispatch(t *testing.T) {
	for _, mode := range []string{"caller-cancel", "close", "cancel-before", "foreign-holder"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			calls := []string{}
			entered, joined := make(chan struct{}), make(chan struct{})
			p, s := windowsLifecycleFixture(t, func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
				mu.Lock()
				calls = append(calls, name)
				mu.Unlock()
				if name == "type" {
					close(entered)
					<-ctx.Done()
					close(joined)
					return nil, context.Cause(ctx)
				}
				payload, _ := decodeObject(args)
				return pixelFixtureReply(name, payload), nil
			})
			if result, err := p.Call(context.Background(), "left_mouse_down", json.RawMessage(`{}`)); err != nil || result.IsError {
				t.Fatal(result, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel-before" || mode == "foreign-holder" {
				if mode == "foreign-holder" {
					if err := os.WriteFile(filepath.Join(s.options.ConfigDir, "computer-use.lock"), encode(map[string]any{"sessionId": "other-live-owner", "pid": os.Getpid()}), 0600); err != nil {
						t.Fatal(err)
					}
				}
				cancel()
				result, err := p.Call(ctx, "key", json.RawMessage(`{"text":"a"}`))
				if err != nil || !result.IsError || !result.NativeCallNotDispatched {
					t.Fatal(result, err)
				}
				if mode == "foreign-holder" && !strings.Contains(result.Content[0].Text, "cleanup was skipped") {
					t.Fatal(result)
				}
			} else {
				done := make(chan NativeToolResult, 1)
				go func() {
					result, err := p.Call(ctx, "type", json.RawMessage(`{"text":"owned"}`))
					if err != nil {
						t.Error(err)
					}
					done <- result
				}()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("actual native mutation did not start")
				}
				if mode == "close" {
					if err := p.Close(context.Background()); err != nil {
						t.Fatal(err)
					}
				} else {
					cancel()
				}
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Fatal("native mutation not joined")
				}
				select {
				case result := <-done:
					if !result.IsError || result.NativeCallNotDispatched || !strings.Contains(result.Content[len(result.Content)-1].Text, "cancelled after dispatch") {
						t.Fatal(result)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled Call did not join")
				}
			}
			mu.Lock()
			ups, keys := 0, 0
			for _, name := range calls {
				if name == "mouse_up" {
					ups++
				}
				if name == "key" {
					keys++
				}
			}
			mu.Unlock()
			expectedUps := 1
			if mode == "foreign-holder" {
				expectedUps = 0
			}
			if ups != expectedUps || keys != 0 {
				t.Fatal("owned cleanup/action counts", calls, ups, keys)
			}
			if mode != "foreign-holder" {
				if err := p.EndTurn(context.Background()); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(s.options.ConfigDir, "computer-use.lock")); !os.IsNotExist(err) {
					t.Fatal("turn retained global lock", err)
				}
			}
		})
	}
}
func TestWindowsPixelCloseSerialQueuedRequestsAndPixelCropFailures(t *testing.T) {
	entered := make(chan struct{})
	p, _ := windowsLifecycleFixture(t, func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		if name == "type" {
			close(entered)
			<-ctx.Done()
			return nil, context.Cause(ctx)
		}
		payload, _ := decodeObject(args)
		return pixelFixtureReply(name, payload), nil
	})
	first, second := make(chan NativeToolResult, 1), make(chan NativeToolResult, 1)
	go func() {
		value, _ := p.Call(context.Background(), "type", json.RawMessage(`{"text":"hold"}`))
		first <- value
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("call not entered")
	}
	go func() {
		value, _ := p.Call(context.Background(), "key", json.RawMessage(`{"text":"must not dispatch"}`))
		second <- value
	}()
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, result := range []<-chan NativeToolResult{first, second} {
		select {
		case value := <-result:
			if !value.IsError {
				t.Fatal(value)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("close left queued call")
		}
	}
	value, err := p.Call(context.Background(), "screenshot", json.RawMessage(`{}`))
	if err != nil || !value.NativeCallNotDispatched {
		t.Fatal(value, err)
	}
}

func TestActualWindowsPixelBinderCancellationMouseOwnershipMatchesSource(t *testing.T) {
	for _, kind := range []string{"before", "after"} {
		t.Run(kind, func(t *testing.T) {
			rows := []map[string]any{{"name": "left_mouse_down", "args": map[string]any{}}}
			if kind == "before" {
				rows = append(rows, map[string]any{"name": "key", "args": map[string]any{"text": "must not dispatch"}, "abortBefore": true})
			} else {
				rows = append(rows, map[string]any{"name": "type", "args": map[string]any{"text": "owned"}, "abortOn": "type"})
			}
			// Use the same actual producer as the normal corpus, retaining the raw
			// private cancellation marker that JavaScript symbols omit from JSON.
			var profile pixelProfile
			data, _ := json.Marshal(map[string]any{"cases": rows})
			_ = json.Unmarshal(data, &profile)
			bun := os.Getenv("CC_HAHA_SOURCE_BUN")
			if bun == "" {
				t.Skip("actual Source Bun required")
			}
			home := t.TempDir()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-windows-pixel.fixture.ts")
			ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			child.Dir = home
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CU_WINDOWS_PIXEL_FIXTURE=1", "PATH=/usr/bin:/bin", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			child.Stdin = bytes.NewReader(encode(map[string]any{"profiles": []any{map[string]any{"cases": rows}}}))
			output, err := child.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			var source struct{ Profiles []pixelProfileResult }
			if err = json.Unmarshal(output, &source); err != nil || len(source.Profiles) != 1 {
				t.Fatal(err, string(output))
			}
			actual := pixelProfileResult{Results: []NativeToolResult{}, Calls: []pixelNativeCall{}}
			var cancel context.CancelFunc
			p, _ := windowsLifecycleFixture(t, func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
				actual.Calls = append(actual.Calls, pixelNativeCall{name, args})
				if kind == "after" && name == "type" {
					cancel()
				}
				payload, _ := decodeObject(args)
				return pixelFixtureReply(name, payload), nil
			})
			value, err := p.Call(context.Background(), "left_mouse_down", json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			actual.Results = append(actual.Results, value)
			callCtx, callCancel := context.WithCancel(context.Background())
			cancel = callCancel
			defer callCancel()
			if kind == "before" {
				cancel()
			}
			row := profile.Cases[1]
			value, err = p.Call(callCtx, row.Name, row.Args)
			if err != nil {
				t.Fatal(err)
			}
			actual.Results = append(actual.Results, value)
			actual.Selected, actual.Pinned = p.selected, p.pinned
			if !reflect.DeepEqual(jsonValue(t, encode(actual)), jsonValue(t, encode(source.Profiles[0]))) {
				t.Fatalf("actual Source=%s\nGo=%s", encode(source.Profiles[0]), encode(actual))
			}
		})
	}
}
