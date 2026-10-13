package streamhistory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestWatchdogStateMatchesActualMainConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	events := []json.RawMessage{
		[]byte(`{}`), []byte(`{"type":"message_start"}`),
		[]byte(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use"}}`),
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`),
		[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\ud800"}}`),
		[]byte(`{"type":"content_block_stop","index":0}`),
		[]byte(`{"type":"content_block_start","index":1,"content_block":{"type":"thinking"}}`),
		[]byte(`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"thinking"}}`),
		[]byte(`{"type":"content_block_start","index":2,"content_block":{"type":"text"}}`),
		[]byte(`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"text"}}`),
		[]byte(`{"type":"content_block_delta","index":2,"delta":{"type":"signature_delta","signature":"signed"}}`),
		[]byte(`{"type":"content_block_start","index":3,"content_block":{"type":"server_tool_use"}}`),
		[]byte(`{"type":"content_block_delta","index":3,"delta":{"type":"connector_text_delta","connector_text":"result"}}`),
		[]byte(`{"type":"message_stop"}`),
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-stream-state.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_STREAM_STATE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	raw, _ := json.Marshal(map[string]any{"home": home, "events": events, "blocks": []string{"text", "tool_use", "text", "server_tool_use"}})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual MAIN %v %s", err, stderr.String())
	}
	var source struct {
		Results []map[string]any
		Commits []map[string]any
	}
	if err = json.Unmarshal(raw, &source); err != nil || len(source.Results) != len(events) {
		t.Fatalf("actual state %v %s", err, raw)
	}
	state := NewWatchdogState()
	for i, event := range events {
		first, err := state.Record(event)
		if err != nil {
			t.Fatal(err)
		}
		errors := []any{}
		for _, reason := range []string{"idle", "max_duration", "tool_input_duration"} {
			e := &WatchdogTimeoutError{Reason: reason, TimeoutMS: 1500, Snapshot: state.Value.Clone()}
			enumerable, err := e.EnumerableErrorJSON()
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := enumerable.MarshalJSON()
			var object any
			_ = json.Unmarshal(encoded, &object)
			diagnostic, err := e.DiagnosticJSON()
			if err != nil {
				t.Fatal(err)
			}
			errors = append(errors, map[string]any{"message": e.Error(), "safe": e.SafeToRetry(), "enumerable": object, "diagnostic": string(diagnostic)})
		}
		observed, _ := json.Marshal(map[string]any{"first": first, "snapshot": state.Value, "errors": errors})
		var got map[string]any
		_ = json.Unmarshal(observed, &got)
		if !reflect.DeepEqual(got, source.Results[i]) {
			t.Fatalf("step %d state changed: %s source %#v", i, observed, source.Results[i])
		}
	}
	// A buffered local call has not run; only a server tool can commit before EOF.
	c, err := New(Options{UUID: func() string { return "id" }, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{"text", "tool_use", "text", "server_tool_use"} {
		yielded := []int{}
		block := &Block{Object: object(json.RawMessage(`{"type":"` + kind + `"}`))}
		e := &Envelope{Content: []*Block{block}, UUID: fmt.Sprint(i)}
		if err = c.add(e, func(u Update) error { n, _ := strconv.Atoi(u.Envelope.UUID); yielded = append(yielded, n); return nil }); err != nil {
			t.Fatal(err)
		}
		observed, _ := json.Marshal(map[string]any{"yielded": yielded, "crossed": c.crossed, "committed": c.committed})
		var got map[string]any
		_ = json.Unmarshal(observed, &got)
		if !reflect.DeepEqual(got, source.Commits[i]) {
			t.Fatalf("commit %d: %s source %#v", i, observed, source.Commits[i])
		}
	}
}
