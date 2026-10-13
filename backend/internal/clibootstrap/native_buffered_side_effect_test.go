package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMainPrintBufferedBashTruncationRetriesBeforeOneSideEffect(t *testing.T) {
	var posts atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch n := posts.Add(1); n {
		case 1:
			// The local tool block is complete, but no final stop/usage frame
			// arrives. A retry must discard it before actual Bash execution.
			recorder := httptest.NewRecorder()
			emitNativeTaskTool(recorder, "physical-bash-counter", "Bash", map[string]any{"command": "printf 'EXACTLY_ONCE\\n' >> " + target, "description": "append one physical counter"})
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, strings.Split(recorder.Body.String(), "event: message_delta")[0])
		case 2:
			emitNativeTaskTool(w, "physical-bash-counter", "Bash", map[string]any{"command": "printf 'EXACTLY_ONCE\\n' >> " + target, "description": "append one physical counter"})
		case 3:
			emitNativeTaskTool(w, "physical-counter-read", "Read", map[string]any{"file_path": target})
		case 4:
			if !strings.Contains(string(body), "EXACTLY_ONCE") {
				t.Error("real side effect result not fed to model")
			}
			emitPlanFixture(w, int(n), "", "PHYSICAL_COUNTER_COMPLETE")
		default:
			t.Error("unbounded stream/tool replay", n)
			http.Error(w, "unexpected request", 500)
		}
	}))
	defer peer.Close()
	main := backgroundAgentMainWithOptions(t, peer.URL, func(native *NativeBoundary) {
		target = filepath.Join(native.CWD, "counter.txt")
		native.Environment["CLAUDE_ENABLE_STREAM_WATCHDOG"] = "0"
		native.Environment["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] = "1"
		native.Environment["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "1"
		native.Environment["CLAUDE_CODE_MAX_RETRIES"] = "1"
	}, func(*MainPrintOptions) {})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "append the counter once, read it, and complete"})})
	if err := main.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "EXACTLY_ONCE\n" || posts.Load() != 4 {
		t.Fatalf("side effect replay: requests%d data%q error%v", posts.Load(), data, err)
	}
	if err := main.Host.WaitTranscriptRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	var canonical string
	_ = filepath.WalkDir(filepath.Join(main.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, main.Controller.SessionID()+".jsonl") {
			canonical = path
		}
		return nil
	})
	if canonical == "" {
		t.Fatal("canonical transcript absent")
	}
	log, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	toolCalls := 0
	for _, line := range bytes.Split(log, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var record protocol.JSONObject
		if err = json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		message, ok := record.Object("message")
		if !ok || record.String("type") != "assistant" {
			continue
		}
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "tool_use" && block.String("id") == "physical-bash-counter" {
				toolCalls++
			}
		}
	}
	if toolCalls != 1 {
		t.Fatal("uncommitted tool leaked into canonical history", toolCalls)
	}
}
