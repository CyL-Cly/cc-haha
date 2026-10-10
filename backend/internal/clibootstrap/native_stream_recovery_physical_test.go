package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestActualMainPrintStreamRetryThenToolsAndNextHumanShareCanonical(t *testing.T) {
	var posts atomic.Int32
	var target string
	output := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		number := posts.Add(1)
		switch number {
		case 1:
			w.Header().Set("content-type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"private before output\"}}\n\n")
		case 2:
			emitNativeTaskTool(w, "private-after-retry-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_RECOVERY_ARTIFACT"})
		case 3, 5:
			if !strings.Contains(string(body), "private-after-retry-write") {
				t.Error("resend/next user lost same real canonical history")
			}
			emitNativeTaskTool(w, fmt.Sprintf("private-after-retry-read-%d", number), "Read", map[string]any{"file_path": target})
		case 4, 6:
			if !strings.Contains(string(body), "ACTUAL_RECOVERY_ARTIFACT") {
				t.Error("real Read output absent")
			}
			emitPlanFixture(w, int(number), "", "ACTUAL_RECOVERY_COMPLETE")
		default:
			t.Error("unexpected physical request", number)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		target = filepath.Join(n.CWD, "real-recovery.txt")
		n.Environment["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "2"
		n.Environment["CLAUDE_ENABLE_STREAM_WATCHDOG"] = "0"
		n.Environment["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] = "1"
	}, func(options *MainPrintOptions) {
		options.OnOutput = func(data []byte) error {
			var record protocol.JSONObject
			if err := record.UnmarshalJSON(data); err != nil {
				return err
			}
			output = append(output, record)
			return nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, text := range []string{"Write and read the actual artifact.", "Read the same artifact in the next user turn."} {
		user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
		if err := m.RunTurn(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	if posts.Load() != 6 {
		t.Fatal("actual resend/normal tools count", posts.Load())
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "ACTUAL_RECOVERY_ARTIFACT" {
		t.Fatal("real artifact", err, string(data))
	}
	recoveries, results := 0, 0
	for _, record := range output {
		if record.String("type") == "system" && record.String("subtype") == "streaming_fallback" {
			recoveries++
			if len(record.OwnProperties()) != 5 || record.String("cause") != "stream_retry" || record.String("session_id") != m.Controller.SessionID() || record.String("uuid") == "" {
				t.Fatal("actual original QueryEngine SDK recovery projection", rawMain(record))
			}
		}
		if record.String("type") == "result" {
			results++
			if record.String("subtype") != "success" {
				t.Fatal("actual normal result", rawMain(record))
			}
		}
	}
	if recoveries != 1 || results != 2 {
		t.Fatal("actual SDK recovery/result count", recoveries, results)
	}
	for _, record := range m.Controller.History() {
		if record.String("subtype") == "streaming_fallback" || strings.Contains(string(rawMain(record)), "private before output") {
			t.Fatal("failed attempt or recovery polluted model/canonical history")
		}
	}
	// The real writer must finish the same session, including tool execution.
	if err := m.Host.WaitTranscriptRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	var canonical string
	_ = filepath.WalkDir(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, m.Controller.SessionID()+".jsonl") {
			canonical = path
		}
		return nil
	})
	if canonical == "" {
		t.Fatal("actual canonical absent")
	}
	log, err := os.ReadFile(canonical)
	if err != nil || !strings.Contains(string(log), "ACTUAL_RECOVERY_ARTIFACT") || strings.Contains(string(log), "streaming_fallback") {
		t.Fatal("real persisted recovery boundary", err)
	}
	var lines []json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("actual writer emitted invalid canonical")
		}
		lines = append(lines, json.RawMessage(line))
	}
	if len(lines) == 0 {
		t.Fatal("empty canonical")
	}
}
