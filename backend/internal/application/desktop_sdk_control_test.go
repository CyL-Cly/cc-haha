package application

import (
	"bufio"
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

func TestSDKControlLifecycleMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	cases := []map[string]any{}
	for _, response := range []json.RawMessage{[]byte(`{"subtype":"success","response":{"real":"ack"}}`), []byte(`{"subtype":"success","response":[1,2]}`), []byte(`{"subtype":"success","response":"ignored"}`), []byte(`{"subtype":"error","error":"native rejected"}`), []byte(`{"subtype":"error","error":false}`), []byte(`{"subtype":"error","error":{"code":1}}`)} {
		cases = append(cases, map[string]any{"request": map[string]string{"subtype": "set_model", "model": "actual-model"}, "response": response})
	}
	for _, name := range []string{"cancel", "close", "noResponse", "missing", "aborted", "cannotSend", "notReady", "lateReady"} {
		cases = append(cases, map[string]any{"request": map[string]string{"subtype": "mcp_status"}, name: true, "response": json.RawMessage(`{"subtype":"success","response":{"ok":true}}`), "timeout": 100})
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-control.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_CONTROL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": cases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	capturePublicGoCoverage(t, cmd)
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	for i, row := range cases {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			d := &DesktopSession{ctx: ctx, emit: func(json.RawMessage) {}, projection: NewProjection()}
			if row["missing"] == true {
				d.closed = true
			}
			if row["aborted"] == true {
				cancel(errors.New("private cancelled"))
			}
			sent := []json.RawMessage{}
			var mu sync.Mutex
			send := func(data json.RawMessage) error {
				message := obj(data)
				mu.Lock()
				sent = append(sent, raw(map[string]any{"type": message.String("type"), "request": message.Get("request")}))
				mu.Unlock()
				go func() {
					if row["cancel"] == true {
						cancel(errors.New("private cancelled"))
						return
					}
					if row["close"] == true {
						_ = d.closeSDKTransport(context.Background())
						return
					}
					if row["noResponse"] == true {
						return
					}
					response := obj(raw(row["response"]))
					response.Set("request_id", raw(message.String("request_id")))
					_ = d.AcceptSDKOutput(context.Background(), frame("control_response", field("response", response)))
				}()
				return nil
			}
			if row["notReady"] != true {
				d.BindSDKResponseTransport(send)
			}
			if row["lateReady"] == true {
				unbind := d.BindSDKResponseTransport(send)
				unbind()
				go func() { time.Sleep(5 * time.Millisecond); d.BindSDKResponseTransport(send) }()
			}
			timeout := 200 * time.Millisecond
			if row["timeout"] != nil {
				timeout = 100 * time.Millisecond
			}
			value, e := d.RequestSDKControl(ctx, raw(row["request"]), timeout, func() bool { return row["cannotSend"] != true })
			message := any(nil)
			if e != nil {
				message = e.Error()
			}
			state := d.sdkRuntime()
			state.mu.Lock()
			pending := len(state.controls)
			state.mu.Unlock()
			mu.Lock()
			actual := raw(map[string]any{"value": value, "error": message, "sent": sent, "callbacks": 0, "pending": pending})
			mu.Unlock()
			var a, b any
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(source.Rows[i], &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("%s\n%s", actual, source.Rows[i])
			}
		})
	}
}

func TestSDKControlRealPrivateSubprocess(t *testing.T) {
	if os.Getenv("CC_HAHA_SDK_CONTROL_CHILD") == "1" {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			request := obj(scanner.Bytes())
			response := frame("control_response", field("response", map[string]any{"subtype": "success", "request_id": request.String("request_id"), "response": map[string]any{"model": obj(request.Get("request")).String("model"), "pid": os.Getpid()}}))
			_, _ = os.Stdout.Write(append(response, '\n'))
		}
		os.Exit(0)
	}
	home := t.TempDir()
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestSDKControlRealPrivateSubprocess$")
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_SDK_CONTROL_CHILD=1"}
	stdin, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	capturePublicGoCoverage(t, cmd)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	d := &DesktopSession{ctx: ctx, emit: func(json.RawMessage) {}, projection: NewProjection()}
	d.BindSDKResponseTransport(func(data json.RawMessage) error { _, e := stdin.Write(append(data, '\n')); return e })
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			_ = d.AcceptSDKOutput(ctx, append(json.RawMessage(nil), scanner.Bytes()...))
		}
	}()
	value, e := d.RequestSDKControl(ctx, []byte(`{"subtype":"set_model","model":"configured-actual-model"}`), time.Second, nil)
	if e != nil || obj(value).String("model") != "configured-actual-model" || obj(value).Get("pid") == nil {
		t.Fatal(e, string(value))
	}
	_ = stdin.Close()
	if e = cmd.Wait(); e != nil {
		t.Fatal(e)
	}
	<-done
	_ = d.closeSDKTransport(ctx)
}
