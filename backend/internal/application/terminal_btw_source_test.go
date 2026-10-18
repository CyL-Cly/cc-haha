package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalBtwActualSourceWholeCommandForkedQueryKeysCountAndUsage(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	rows := []map[string]any{{"mode": "usage", "args": "", "count": 7}, {"mode": "usage", "args": "", "count": 7, "fullscreen": true}, {"mode": "usage-idle", "args": "", "count": 7}, {"mode": "usage", "args": " \t ", "count": 7}, {"mode": "answer", "width": 100, "keys": []string{"\x1b[A", "\x1b[B", " "}, "count": 2}, {"mode": "answer", "width": 47, "keys": []string{"\r"}, "count": "2"}, {"mode": "answer", "width": 24, "keys": []string{"\x1b"}, "count": nil}, {"mode": "hold", "keys": []string{"\x03"}}, {"mode": "hold", "keys": []string{"\x04"}}, {"mode": "empty", "keys": []string{" "}}}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-btw.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BTW_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "COLUMNS=100", "LINES=35"}
	for _, count := range []any{true, []any{"a", nil}, map[string]any{"future": true}, json.RawMessage(`"\ud800"`)} {
		rows = append(rows, map[string]any{"mode": "answer", "keys": []string{" "}, "count": count})
	}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual BTW command Source %v %.10000s", err, output)
	}
	var expected struct {
		Declaration protocol.JSONObject
		Rows        []protocol.JSONObject
		Blocked     []string
	}
	if err = json.Unmarshal(output, &expected); err != nil || len(expected.Rows) != len(rows) {
		t.Fatal(err, string(output))
	}
	if expected.Declaration.String("name") != "btw" || expected.Declaration.String("type") != "local-jsx" || string(expected.Declaration.Get("immediate")) != "true" || len(expected.Blocked) > 0 {
		t.Fatal("Source registration/capabilities", expected)
	}
	var heldMu sync.Mutex
	var heldStarted, heldRelease chan struct{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("SOURCE_BTW_BUSY_PARENT")) {
			heldMu.Lock()
			started, release := heldStarted, heldRelease
			heldMu.Unlock()
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		response(w, "btw-parent", "", nil, "unused")
	}))
	defer peer.Close()
	for i, row := range rows {
		o := nativeSessionOptions(t, peer.URL)
		o.Interactive = true
		if row["fullscreen"] == true {
			o.Environment["CLAUDE_CODE_NO_FLICKER"] = "1"
			o.Native.Environment["CLAUDE_CODE_NO_FLICKER"] = "1"
		}
		s, err := NewSession(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close(context.Background())
		want := expected.Rows[i]
		if string(want.Get("parentUnchanged")) != "true" || string(want.Get("parentAborted")) != "false" {
			t.Fatal("Source interrupted parent", i, want)
		}
		count := row["count"]
		if _, ok := row["count"]; !ok {
			count = 0
		}
		receipt, err := s.process.Owner.SubmitIndependent(ctx, cooperative.Input, func(*cooperative.Scope) error {
			return s.process.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				next.Set("btwUseCount", raw(count))
				next.Set("futureBtw", raw(map[string]any{"keep": true}))
				return &next, nil
			})
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			t.Fatal(err)
		}
		args := "owned side question"
		if row["mode"] == "usage" || row["mode"] == "usage-idle" {
			args = row["args"].(string)
		}
		question, err := s.process.PrepareTerminalBtw(ctx, args)
		if err != nil {
			t.Fatal(i, err)
		}
		var actual protocol.JSONObject
		receipt, err = s.process.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error {
			global, e := s.process.Global.Get()
			if e == nil {
				actual = global.Clone()
			}
			return e
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			t.Fatal(err)
		}
		future, futureErr := protocol.ParseAndStringifyJavaScriptJSON(actual.Get("futureBtw"))
		if string(actual.Get("btwUseCount")) != string(want.Get("count")) || futureErr != nil || string(future) != `{"keep":true}` {
			t.Fatalf("Actual Global count/save differs from whole Source profile%d GoCount=%s SourceCount=%s GoFuture=%s Source=%s", i, actual.Get("btwUseCount"), want.Get("count"), actual.Get("futureBtw"), raw(want))
		}
		if question == "" {
			busy := row["mode"] == "usage"
			if busy && (string(want.Get("parentPendingAtCompletion")) != "true" || string(want.Get("calls")) != "1") {
				t.Fatal("Whole Source empty BTW failed while actual parent HTTP is held", i, want)
			}
			heldMu.Lock()
			heldStarted, heldRelease = make(chan struct{}), make(chan struct{})
			started, release := heldStarted, heldRelease
			heldMu.Unlock()
			parentDone := make(chan error, 1)
			if busy {
				go func() {
					parentDone <- s.Submit(ctx, tui.Input{Mode: "prompt", Text: "SOURCE_BTW_BUSY_PARENT"}, func(tui.Event) error { return nil })
				}()
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("Native actual busy parent did not enter")
				}
			}
			events := []tui.Event{}
			emit := func(event tui.Event) error { events = append(events, event); return nil }
			handled, err := s.InvokeTerminalCommand(ctx, "btw", args, emit)
			if !handled || err != nil {
				t.Fatal(i, handled, err)
			}
			records := want.Get("result")
			var result protocol.JSONObject
			_ = result.UnmarshalJSON(records)
			var sourceMessages []protocol.JSONObject
			_ = json.Unmarshal(result.Get("messages"), &sourceMessages)
			offset := 0
			if busy {
				offset = 1
				if len(events) < 1 || events[0].Kind != tui.TerminalImmediateNotice || events[0].Text != "Usage: /btw <your question>" {
					t.Fatal("Actual immediate Source notice", i, events)
				}
			}
			if busy && row["fullscreen"] == true {
				if len(sourceMessages) != 0 || len(events) != 1 {
					t.Fatal("Actual fullscreen omits local replay records", i, sourceMessages, events)
				}
			} else if len(sourceMessages) != 2 || len(events) != offset+1 || events[offset].Kind != tui.LocalProcessedMessages || len(events[offset].LocalMessages) != 2 {
				t.Fatal("Actual full caller canonical records", i, string(records), events)
			}
			for j, message := range sourceMessages {
				if message.String("type") != "system" || message.String("subtype") != "local_command" || message.String("content") != events[offset].LocalMessages[j].Text {
					t.Fatal("Canonical Usage Source mismatch", i, j, message, events)
				}
			}
			if busy {
				close(release)
				select {
				case e := <-parentDone:
					if e != nil {
						t.Fatal(e)
					}
				case <-ctx.Done():
					t.Fatal("Original parent failed to finish")
				}
			}
		} else {
			if string(want.Get("calls")) != "1" {
				t.Fatal("Whole Source did not execute actual model", i, want)
			}
			var events [][]json.RawMessage
			_ = json.Unmarshal(want.Get("events"), &events)
			if len(events) != 1 || len(events[0]) != 2 || string(events[0][0]) != "null" || string(events[0][1]) != `{"display":"skip"}` {
				t.Fatal("Actual Source close/skip keys", i, events)
			}
		}
	}
}
