package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func emitForkSkillMultiBlockSSE(w http.ResponseWriter, files []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event string, value any) {
		data, _ := json.Marshal(value)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	}
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "fork-multiblock-message", "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
	for index := 0; index < 4; index++ {
		if index%2 == 0 {
			send("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}})
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": "FORK_CHILD_TEXT_NOT_PROGRESS"}})
		} else {
			ordinal := index / 2
			input, _ := json.Marshal(map[string]any{"file_path": files[ordinal], "content": fmt.Sprintf("FORK_MULTI_ARTIFACT_%d", ordinal)})
			send("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": fmt.Sprintf("fork-multi-write-%d", ordinal), "name": "Write", "input": map[string]any{}}})
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}})
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
	}
	send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": 4}})
	send("message_stop", map[string]any{"type": "message_stop"})
}

func TestActualForkedSkillMultiBlockSSEProgressKeepsOriginalOrderUUIDAndParent(t *testing.T) {
	var parent, child atomic.Int32
	var files []string
	var wire []string
	uuids := map[string]string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "FORK_PRIVATE_CHILD_SYSTEM") {
			if child.Add(1) == 1 {
				emitForkSkillMultiBlockSSE(w, files)
				return
			}
			if !bytes.Contains(body, []byte("fork-multi-write-0")) || !bytes.Contains(body, []byte("fork-multi-write-1")) {
				t.Error("child lost actual multiple Write results", string(body))
			}
			emitPlanFixture(w, 2, "", "FORK_MULTI_DONE")
			return
		}
		if parent.Add(1) == 1 {
			emitNativeTaskTool(w, "parent-skill", "Skill", map[string]any{"skill": "forkprobe", "args": "ARG_TOKEN"})
			return
		}
		if !bytes.Contains(body, []byte("FORK_MULTI_DONE")) || bytes.Contains(body, []byte("fork-multi-write-")) || bytes.Contains(body, []byte("FORK_CHILD_TEXT_NOT_PROGRESS")) {
			t.Error("parent API history mixed progress with model input", string(body))
		}
		emitPlanFixture(w, 3, "", "PARENT_MULTI_DONE")
	}))
	defer peer.Close()
	m := forkSkillMainWithParentSkillGrant(t, peer.URL, true, func(o *MainPrintOptions) {
		o.OnOutput = func(data []byte) error {
			var frame protocol.JSONObject
			if err := frame.UnmarshalJSON(data); err != nil {
				return err
			}
			if frame.String("subtype") == "agent_run_message" || frame.String("type") == "progress" {
				return fmt.Errorf("Skill public output became Agent/raw progress: %s", data)
			}
			if frame.String("parent_tool_use_id") != "parent-skill" {
				return nil
			}
			api, _ := frame.Object("message")
			var blocks []protocol.JSONObject
			if err := json.Unmarshal(api.Get("content"), &blocks); err != nil {
				return err
			}
			if len(blocks) != 1 {
				return fmt.Errorf("Skill progress SDK frame was not single-block: %s", data)
			}
			block := blocks[0]
			kind := block.String("type")
			id := block.String("id")
			if kind == "tool_result" {
				id = block.String("tool_use_id")
			}
			if kind != "tool_use" && kind != "tool_result" {
				return fmt.Errorf("Skill text block was emitted as tool progress: %s", data)
			}
			key := kind + ":" + id
			if _, exists := uuids[key]; exists {
				return fmt.Errorf("duplicate Skill progress %s", key)
			}
			uuid := frame.String("uuid")
			if uuid == "" {
				return fmt.Errorf("Skill progress lost child UUID")
			}
			uuids[key] = uuid
			wire = append(wire, key)
			return nil
		}
	})
	files = []string{filepath.Join(m.options.Native.CWD, "fork-multi-a.txt"), filepath.Join(m.options.Native.CWD, "fork-multi-b.txt")}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Invoke forkprobe with ARG_TOKEN"})})); err != nil {
		t.Fatal(err)
	}
	expected := []string{"tool_use:fork-multi-write-0", "tool_use:fork-multi-write-1", "tool_result:fork-multi-write-0", "tool_result:fork-multi-write-1"}
	if !reflect.DeepEqual(wire, expected) || parent.Load() != 2 || child.Load() != 2 {
		t.Fatalf("actual multi-block Skill sequence %v parent=%d child=%d", wire, parent.Load(), child.Load())
	}
	for i, path := range files {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != fmt.Sprintf("FORK_MULTI_ARTIFACT_%d", i) {
			t.Fatalf("actual Write%d missing: %q %v", i, data, err)
		}
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		retained := []string{}
		for _, record := range m.Controller.History() {
			if record.String("type") != "progress" {
				continue
			}
			data, _ := record.Object("data")
			if data.String("type") != "skill_progress" {
				continue
			}
			nested, _ := data.Object("message")
			api, _ := nested.Object("message")
			var blocks []protocol.JSONObject
			if err := json.Unmarshal(api.Get("content"), &blocks); err != nil {
				return err
			}
			if len(blocks) != 1 {
				return fmt.Errorf("internal Skill progress not normalized")
			}
			block := blocks[0]
			id := block.String("id")
			if block.String("type") == "tool_result" {
				id = block.String("tool_use_id")
			}
			key := block.String("type") + ":" + id
			if record.String("parentToolUseID") != "parent-skill" || nested.String("uuid") != uuids[key] {
				return fmt.Errorf("SDK normalization changed retained child UUID/parent: %s", rawMain(record))
			}
			retained = append(retained, key)
		}
		if !reflect.DeepEqual(retained, expected) {
			return fmt.Errorf("mutable Skill progress order %v", retained)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	parents, err := filepath.Glob(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", m.Controller.SessionID()+".jsonl"))
	if err != nil || len(parents) != 1 {
		t.Fatal("parent transcript", parents, err)
	}
	disk, err := os.ReadFile(parents[0])
	if err != nil || bytes.Contains(disk, []byte("fork-multi-write-")) || bytes.Contains(disk, []byte(`"type":"progress"`)) {
		t.Fatalf("progress entered parent disk chain: %v %s", err, disk)
	}
}

func TestActualForkedSkillProgressConsumerErrorKeepsIdentityAndJoinsChild(t *testing.T) {
	failure := errors.New("owned actual Skill SDK consumer failed")
	var parent, child atomic.Int32
	var artifact string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "FORK_PRIVATE_CHILD_SYSTEM") {
			child.Add(1)
			emitNativeTaskTool(w, "fork-error-write", "Write", map[string]any{"file_path": artifact, "content": "MUST_NOT_WRITE_AFTER_EMIT_ERROR"})
			return
		}
		if parent.Add(1) == 1 {
			emitNativeTaskTool(w, "parent-skill", "Skill", map[string]any{"skill": "forkprobe", "args": "ARG_TOKEN"})
			return
		}
		emitPlanFixture(w, 2, "", "PARENT_ERROR_FINISHED")
	}))
	defer peer.Close()
	var delivered atomic.Int32
	m := forkSkillMainWithParentSkillGrant(t, peer.URL, true, func(o *MainPrintOptions) {
		o.OnOutput = func(data []byte) error {
			var frame protocol.JSONObject
			if err := frame.UnmarshalJSON(data); err != nil {
				return err
			}
			if frame.String("parent_tool_use_id") == "parent-skill" {
				delivered.Add(1)
				return failure
			}
			return nil
		}
	})
	artifact = filepath.Join(m.options.Native.CWD, "fork-consumer-error.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Invoke forkprobe with ARG_TOKEN"})}))
	if !errors.Is(err, failure) || delivered.Load() != 1 || child.Load() != 1 {
		t.Fatalf("Skill progress consumer error identity/once changed: %v delivered=%d child=%d", err, delivered.Load(), child.Load())
	}
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Fatal("child tool ran after progress consumer throw", err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal("actual child cleanup after progress rejection", err)
	}
}
