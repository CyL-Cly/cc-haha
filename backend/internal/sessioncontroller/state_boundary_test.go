package sessioncontroller

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualRestorationFailurePriority(t *testing.T) {
	for _, name := range []string{"bad-resume-dependency", "bad-writer-dependency", "bad-settings", "bad-member-identity", "bad-read-path", "bad-snapshot"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			options := hostOptions(t, home).options
			var input = activation()
			switch name {
			case "bad-resume-dependency":
				options.ResumeOptions.Loader = nil
			case "bad-writer-dependency":
				options.WriterOptions.Files = nil
			case "bad-settings":
				writePrivate(t, filepath.Join(home, ".claude", "settings.json"), `{"model":3}`)
			case "bad-member-identity":
				path := filepath.Join(home, ".claude", "projects", sanitize(home), "11111111-1111-4111-8111-111111111111.jsonl")
				writePrivate(t, path, `{"parentUuid":null,"type":"user","uuid":3,"sessionId":"11111111-1111-4111-8111-111111111111","timestamp":"2025-01-01T00:00:00Z","message":{"role":"user","content":"q"}}`+"\n")
				input.Resume.Source = quote("11111111-1111-4111-8111-111111111111")
			case "bad-read-path":
				input.Resume.Source = []byte(`{"messages":[{"type":"assistant","uuid":"a","timestamp":"2025-01-01T00:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"id","name":"Read","input":{"file_path":3}}]}},{"type":"user","uuid":"u","timestamp":"2025-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"id","content":"q"}]}},{"type":"assistant","uuid":"final","timestamp":"2025-01-01T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}]}`)
			case "bad-snapshot":
				options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
				input.Resume.Source = []byte(`{"messages":[],"fileHistorySnapshots":[{"trackedFileBackups":null}]}`)
			}
			controller, err := New(options)
			if err == nil {
				err = controller.Activate(input)
				if controller != nil {
					controller.AwaitBackground()
				}
			}
			if err == nil {
				t.Fatal("unsupported original restore accepted")
			}
		})
	}
}
func TestFileHistoryStateConsumerAndTodoTaskGate(t *testing.T) {
	controller := activeController(t)
	controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
	home := controller.options.ResumeOptions.OriginalCWD
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "fileHistorySnapshots", Value: raw([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "messageId", Value: quote("original")}, protocol.JSONField{Name: "schemaVersion", Value: raw(2)}, protocol.JSONField{Name: "trackedFileBackups", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: filepath.Join(home, "inside"), Value: raw(nil)}, protocol.JSONField{Name: "/outside/original", Value: raw(nil)}))})})})
	if err := controller.restoreAppState(metadata, nil); err != nil {
		t.Fatal(err)
	}
	state := controller.AppState()
	if string(state.FileHistory.Get("trackedFiles")) != `["inside","/outside/original"]` {
		t.Fatal(string(state.FileHistory.Get("trackedFiles")))
	}
	state.FileHistory.Set("snapshotSequence", raw(9))
	if string(controller.AppState().FileHistory.Get("snapshotSequence")) != "1" {
		t.Fatal("AppState observer mutated host")
	}
	controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "true"
	history := toolMessages("TodoWrite", []byte(`{"todos":[{"content":"task","status":"pending","activeForm":"doing"}]}`), quote("ok"), nil)
	if err := controller.restoreAppState(protocol.NewJSONObject(), history); err != nil || len(controller.AppState().Todos.OwnProperties()) != 0 {
		t.Fatal("task-v2 parsed legacy todos")
	}
	controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "false"
	history = toolMessages("TodoWrite", []byte(`{"todos":3}`), quote("ok"), nil)
	if err := controller.restoreAppState(protocol.NewJSONObject(), history); err != nil {
		t.Fatal(err)
	}
	controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "false"
	controller.options.ResumeOptions.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"] = "true"
	if err := controller.restoreAppState(metadata, nil); err != nil {
		t.Fatal(err)
	}
	controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
	controller.options.ResumeOptions.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"] = "false"
	if _, err := controller.AdmitOriginalBatch([]protocol.JSONObject{object([]byte(`{"type":"user","message":{"content":"new"}}`))}); err == nil {
		t.Fatal("unported active snapshot creation escaped")
	}
}
func TestRecordParentCompactAndExcludedAttachments(t *testing.T) {
	writer := readyWriter(t, OSFiles{})
	u := plainMessage("u", "q")
	a := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("assistant")}, protocol.JSONField{Name: "uuid", Value: quote("assistant")}, protocol.JSONField{Name: "message", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: raw([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text")}, protocol.JSONField{Name: "text", Value: quote("answer")})})}))})
	if _, err := writer.Record([]*protocol.JSONObject{u, &a}, nil); err != nil {
		t.Fatal(err)
	}
	compact := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("system")}, protocol.JSONField{Name: "subtype", Value: quote("compact_boundary")}, protocol.JSONField{Name: "uuid", Value: quote("compact")})
	next := plainMessage("next", "next")
	next.Set("sourceToolAssistantUUID", quote("assistant"))
	if _, err := writer.Record([]*protocol.JSONObject{u, &a, &compact, next}, quote("prompt")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(writer.path)
	if !strings.Contains(string(data), `"logicalParentUuid":"assistant"`) || !strings.Contains(string(data), `"parentUuid":"assistant","isSidechain":false,"promptId":"prompt","type":"user","uuid":"next"`) {
		t.Fatal(string(data))
	}
	// A valid unlogged attachment is filtered. Missing attachment objects throw
	// Source's TypeError, covered by the actual Source matrix separately.
	noise := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("attachment")}, protocol.JSONField{Name: "attachment", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("future")}))})
	progress := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("progress")})
	if parent, err := writer.Record([]*protocol.JSONObject{&noise, &progress}, nil); err != nil || string(parent) != "null" {
		t.Fatal("progress/attachment participated in chain")
	}
	if parent, err := writer.Record([]*protocol.JSONObject{u, &a, &compact, next}, nil); err != nil || text(parent) != "next" {
		t.Fatal("existing prefix parent lost")
	}
	bad := plainMessage("bad", "q")
	bad.Set("uuid", raw(3))
	if _, err := writer.Record([]*protocol.JSONObject{bad}, nil); err == nil {
		t.Fatal("non-string native UUID accepted")
	}
	next = plainMessage("invalid-source", "q")
	next.Set("sourceToolAssistantUUID", raw(3))
	if _, err := writer.Record([]*protocol.JSONObject{next}, nil); err == nil {
		t.Fatal("non-string parent override accepted")
	}
	meta := plainMessage("meta", "skip")
	meta.Set("isMeta", raw(true))
	array := plainMessage("array", "")
	array.Set("message", raw(protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: raw([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text")}, protocol.JSONField{Name: "text", Value: quote(strings.Repeat("x", 220))})})})))
	if _, err := writer.Record([]*protocol.JSONObject{meta, array}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(text(writer.lastPrompt), "…") {
		t.Fatal("meaningful prompt budget changed")
	}
}

func TestDrainTimerPolicyAndOriginalJSONFailures(t *testing.T) {
	files := &faultFiles{}
	writer := readyWriter(t, files)
	var callback func()
	writer.options.Schedule = func(_ time.Duration, fn func()) func() { callback = fn; return func() {} }
	if _, err := writer.Record([]*protocol.JSONObject{plainMessage("u", "q")}, nil); err != nil {
		t.Fatal(err)
	}
	files.open = syscall.EACCES
	seen := false
	writer.options.OnError = func(err error) { seen = errors.Is(err, syscall.EACCES) }
	callback()
	if !seen {
		t.Fatal("async native drain error not reported")
	}
	writer = readyWriter(t, OSFiles{})
	if _, err := writer.Record([]*protocol.JSONObject{plainMessage("u", "q")}, nil); err != nil {
		t.Fatal(err)
	}
	writer.options.SessionPersistenceDisabled = true
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(writer.path)
	if len(data) != 0 {
		t.Fatal("late policy change wrote queued history")
	}
	writer.options.SessionPersistenceDisabled = false
	writer.AdoptSessionFile()
	invalid := protocol.NewJSONObject(protocol.JSONField{Name: "future", Value: json.RawMessage(`{broken`)})
	if err := writer.Append(&invalid); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err == nil {
		t.Fatal("invalid original JSON silently serialized")
	}
	files = &faultFiles{read: syscall.EIO}
	writer = readyWriter(t, files)
	writer.AdoptSessionFile()
	writer.RestoreMetadata(object([]byte(`{"customTitle":"cached","tag":"cached"}`)))
	if err := writer.RefreshMetadata(); err != nil {
		t.Fatal("tail-read error should leave cache authoritative", err)
	}
}

type identityInfo struct {
	fs.FileInfo
	value any
}

func (info identityInfo) Sys() any { return info.value }
func TestNativeIdentityDoesNotInventPathEquality(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "file")
	writePrivate(t, path, "a")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fileIdentity(identityInfo{info, nil}); err == nil {
		t.Fatal("unsupported host descriptor invented")
	}
	if value, err := fileIdentity(identityInfo{info, struct{ Dev, Ino int64 }{1, 2}}); err != nil || value.device != 1 || value.inode != 2 {
		t.Fatal(value, err)
	}
	if _, err = fileIdentity(identityInfo{info, struct{ Dev, Ino string }{"1", "2"}}); err == nil {
		t.Fatal("string native identity accepted")
	}
	if _, err = fileIdentity(identityInfo{info, struct{ Dev int64 }{1}}); err == nil {
		t.Fatal("missing inode accepted")
	}
}

func TestUnsupportedStateCoercionAndUTF16PathFailClosed(t *testing.T) {
	for _, name := range []string{"array", "schema", "surrogate", "NUL", "tracked"} {
		t.Run(name, func(t *testing.T) {
			controller := activeController(t)
			controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
			source := `{"fileHistorySnapshots":[{"schemaVersion":2,"trackedFileBackups":{}}]}`
			switch name {
			case "array":
				source = `{"fileHistorySnapshots":3}`
			case "schema":
				source = `{"fileHistorySnapshots":[{"schemaVersion":"2","trackedFileBackups":{}}]}`
			case "surrogate":
				source = `{"fileHistorySnapshots":[{"schemaVersion":2,"trackedFileBackups":{"\ud800":null}}]}`
			case "NUL":
				source = `{"fileHistorySnapshots":[{"schemaVersion":2,"trackedFileBackups":{"nul\u0000path":null}}]}`
			case "tracked":
				source = `{"fileHistorySnapshots":[{"schemaVersion":2,"trackedFileBackups":null}]}`
			}
			before := marshal(t, controller.AppState())
			if err := controller.restoreAppState(object([]byte(source)), nil); err == nil {
				t.Fatal("unsupported original state accepted")
			}
			if after := marshal(t, controller.AppState()); before != after {
				t.Fatal("failed restore changed state", after)
			}
		})
	}
	home := t.TempDir()
	options := hostOptions(t, home).options
	options.WriterOptions.CWD = "/" + strings.Repeat("x", 201)
	longController, err := New(options)
	if err != nil {
		t.Fatal("Source-proven long project path rejected", err)
	}
	want := filepath.Join(options.WriterOptions.ConfigDir, "projects", projectpath.Sanitize(options.WriterOptions.CWD), options.WriterOptions.SessionID+".jsonl")
	if got := longController.writer.target(); got != want || len(filepath.Base(filepath.Dir(got))) <= 200 {
		t.Fatal("long constructor path did not use shared Source-proven hash", got, want)
	}
	if _, err = longController.writer.Record([]*protocol.JSONObject{plainMessage("source-supported-long-path", "original constructor input")}, nil); err != nil {
		t.Fatal("long constructor did not materialize actual file", err)
	}
	if err = longController.writer.Flush(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(want)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("long constructor native mode", info, err)
	}
	if rows := readRows(t, want); len(rows) != 1 || rows[0].String("uuid") != "source-supported-long-path" {
		t.Fatal("long constructor real JSONL", rows)
	}
	controller := activeController(t)
	if _, err := controller.AdmitOriginalBatch([]protocol.JSONObject{object([]byte(`{"type":"user","message":{"content":"\ufeff  /command"}}`))}); err == nil {
		t.Fatal("trimmed slash dispatched as prompt")
	}
	if len(controller.QueueEvents()) != 0 {
		t.Fatal("capability guard queued unsupported prompt")
	}
}
func TestLazyOriginalMembershipReadsActualTranscriptAndRetainsCloneOwnership(t *testing.T) {
	controller := activeController(t)
	path := controller.writer.target()
	writePrivate(t, path, `{"parentUuid":null,"type":"user","uuid":"persisted-member","sessionId":"11111111-1111-4111-8111-111111111111","timestamp":"2025-01-01T00:00:00Z","message":{"role":"user","content":"q"}}`+"\n")
	results, err := controller.AdmitOriginalBatch([]protocol.JSONObject{object([]byte(`{"type":"user","uuid":"persisted-member","message":{"content":"retry"}}`)), object([]byte(`{"type":"user","uuid":"new-original","message":{"content":"new"}}`))})
	if err != nil || !results[0].Historical || results[1].Duplicate {
		t.Fatal(results, err)
	}
	checks := controller.Replays()
	checks[0].Set("uuid", quote("changed"))
	if text(controller.Replays()[0].Get("uuid")) != "persisted-member" {
		t.Fatal("observer altered acknowledgement cache")
	}
	controller.options.ReplayUserMessages = false
	if result, err := controller.AdmitSDK(object([]byte(`{"type":"user","uuid":"new-original","message":{"content":"retry"}}`))); err != nil || !result.Duplicate || result.Acknowledgement != nil {
		t.Fatal(result, err)
	}
	if _, err := controller.AcceptNext(); err != nil {
		t.Fatal(err)
	}
	observed := controller.QueueEvents()
	observed[0].Set("value", quote("changed"))
	if text(controller.QueueEvents()[0].Get("value")) != "new" {
		t.Fatal("observer altered original queue")
	}
	if _, err := controller.AdmitSDK(object([]byte(`{"type":"user","uuid":"unsupported-second","message":{"content":"new"}}`))); err == nil {
		t.Fatal("second fresh SDK accepted")
	}
	key, _ := stringKey(quote("unsupported-second"))
	if controller.received[key] {
		t.Fatal("rejected fresh SDK polluted UUID cache")
	}

	controller = activeController(t)
	writePrivate(t, controller.writer.target(), `{"parentUuid":null,"type":"user","uuid":"snip","snipMetadata":{"removedUuids":{"future":true}},"message":{"role":"user","content":"q"}}`+"\n")
	if _, err := controller.AdmitOriginalBatch([]protocol.JSONObject{object([]byte(`{"type":"user","uuid":"original","message":{"content":"new"}}`))}); err == nil {
		t.Fatal("unsupported original snip iteration reached admission")
	}
	if len(controller.QueueEvents()) != 0 {
		t.Fatal("lazy load error reached admission")
	}

}
func TestMetadataDrainRetainsNativeFailureOwnership(t *testing.T) {
	for _, fault := range []string{"open", "stat", "close", "write"} {
		t.Run(fault, func(t *testing.T) {
			files := &faultFiles{}
			writer := readyWriter(t, files)
			if _, err := writer.Record([]*protocol.JSONObject{plainMessage("u", "q")}, nil); err != nil {
				t.Fatal(err)
			}
			if err := writer.Flush(); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "open":
				files.open = syscall.EACCES
			case "stat":
				files.stat = syscall.EIO
			case "close":
				files.close = syscall.EIO
			case "write":
				files.write = syscall.ENOSPC
			}
			if err := writer.RefreshMetadata(); err == nil {
				t.Fatal("native metadata drain error disappeared")
			}
		})
	}
}
