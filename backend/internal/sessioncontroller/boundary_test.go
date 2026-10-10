package sessioncontroller

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
)

func activation() Activation {
	return Activation{Resume: resumestate.Input{Source: []byte(`{"messages":[],"sessionId":"11111111-1111-4111-8111-111111111111"}`)}}
}
func activeController(t *testing.T) *Controller {
	t.Helper()
	home := t.TempDir()
	options := hostOptions(t, home).options
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.Activate(activation()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.AwaitBackground)
	return controller
}
func TestOriginalCapabilityRefusals(t *testing.T) {
	for _, name := range []string{"factory", "UUID", "clock", "observer", "tools", "system", "internal", "hook"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			host := hostOptions(t, home)
			options := host.options
			switch name {
			case "factory":
				options.Factory = nil
			case "UUID":
				options.UUID = nil
			case "clock":
				options.Now = nil
			case "observer":
				options.OnPhase = nil
			case "tools":
				options.BaseToolsCLI = []string{"Read"}
			case "system":
				options.CustomSystemPrompt = []byte("null")
			case "internal":
				options.ResumeOptions.Environment["USER_TYPE"] = "ant"
			case "hook":
				options.ResumeOptions.Environment["CLAUDE_CODE_SAVE_HOOK_ADDITIONAL_CONTEXT"] = "yes"
			}
			if _, err := New(options); err == nil {
				t.Fatal("unsupported host accepted")
			}
			entries, _ := os.ReadDir(home)
			if len(entries) != 0 {
				t.Fatal("preflight touched files")
			}
		})
	}
	controller := activeController(t)
	if _, err := controller.Prepare(requestcontext.Context{History: []protocol.JSONObject{}}, requestcontext.Dependencies{}); err == nil {
		t.Fatal("Source prepared history accepted")
	}
	if _, err := controller.Prepare(requestcontext.Context{Tools: []protocol.JSONObject{protocol.NewJSONObject()}}, requestcontext.Dependencies{}); err == nil {
		t.Fatal("tools escaped explicit profile")
	}
	if err := controller.Activate(activation()); err == nil {
		t.Fatal("second startup accepted")
	}
	for _, input := range []string{`{"type":"control_request"}`, `{"type":"user","message":{"content":[]}}`, `{"type":"user","message":{"content":"/command"}}`, `{"type":"user","message":{"content":"plain"},"uuid":{}}`, `{"type":"user","message":{"content":"plain"},"priority":"unknown"}`} {
		if _, err := controller.AdmitSDK(object([]byte(input))); err == nil {
			t.Fatalf("unsupported original SDK admitted: %s", input)
		}
	}
}
func TestActivationFailureAndInactiveAdmission(t *testing.T) {
	home := t.TempDir()
	options := hostOptions(t, home).options
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = controller.AdmitSDK(protocol.NewJSONObject()); err == nil {
		t.Fatal("inactive SDK accepted")
	}
	if _, err = controller.AcceptNext(); err == nil {
		t.Fatal("inactive queue accepted")
	}
	if _, err = controller.Prepare(requestcontext.Context{}, requestcontext.Dependencies{}); err == nil {
		t.Fatal("inactive request accepted")
	}
	if err = controller.Activate(Activation{Resume: resumestate.Input{Source: quote("11111111-1111-4111-8111-111111111111")}}); err == nil {
		t.Fatal("missing original session accepted")
	}
	if err = controller.Activate(Activation{Resume: activation().Resume, ResumeAt: "missing"}); err == nil || !strings.Contains(err.Error(), "No message found") {
		t.Fatal(err)
	}
}
func TestRuntimeUUIDCacheQueuePriorityAndBatch(t *testing.T) {
	controller := activeController(t)
	for _, row := range []struct{ id, priority, content string }{{"a", "later", "later"}, {"b", "now", "now"}, {"c", "next", "next"}} {
		if err := controller.enqueue(command{Content: quote(row.content), UUID: quote(row.id), Priority: row.priority}); err != nil {
			t.Fatal(err)
		}

	}
	message, err := controller.AcceptNext()
	if err != nil {
		t.Fatal(err)
	}
	if text(object(message.Get("message")).Get("content")) != "now\nnext\nlater" || text(message.Get("uuid")) != "a" {
		t.Fatal(marshal(t, message))
	}
	if len(controller.Replays()) != 2 {
		t.Fatal("batch acknowledgements lost")
	}
	if message, err = controller.AcceptNext(); err != nil || message != nil {
		t.Fatal("queue was not emptied")
	}
	controller = activeController(t)
	if _, err := controller.AdmitSDK(object([]byte(`{"type":"user","uuid":"b","message":{"content":"original"}}`))); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := controller.AdmitSDK(object([]byte(`{"type":"user","uuid":"b","message":{"content":"retry"}}`))); err != nil || !duplicate.Duplicate {
		t.Fatal("runtime duplicate escaped", err)
	}
	controller.newSDKAdmitted = false
	controller.options.ReplayUserMessages = false
	if duplicate, err := controller.AdmitSDK(object([]byte(`{"type":"user","uuid":"b","message":{"content":"retry"}}`))); err != nil || duplicate.Acknowledgement != nil {
		t.Fatal("disabled replay emitted")
	}
	// Original UUID limit evicts exactly the oldest received item. No turns or
	// transcript business is mocked; each SDK command is genuinely admitted.
	controller = activeController(t)
	for index := 0; index <= 10000; index++ {
		controller.trackReceived(string(quote(fmtInt(index))))
	}

	if len(controller.received) != 10000 || controller.received[string(quote("0"))] {
		t.Fatal("received UUID cache lifetime diverged")
	}
	controller = activeController(t)
	if err := controller.enqueue(command{Content: quote("queued"), Priority: "next"}); err != nil {
		t.Fatal(err)
	}
	copy := controller.QueueEvents()
	copy[0].Set("value", quote("mutation"))
	if text(controller.QueueEvents()[0].Get("value")) != "queued" {
		t.Fatal("queue observer mutated state")
	}

}
func fmtInt(value int) string {
	if value == 0 {
		return "0"
	}
	data := []byte{}
	for value > 0 {
		data = append([]byte{byte(value%10) + '0'}, data...)
		value /= 10
	}
	return string(data)
}

type faultFiles struct {
	OSFiles
	open, stat, mkdir, read, write, close, chmod error
	zero                                         bool
}

func (f *faultFiles) OpenFile(path string, flag int, mode fs.FileMode) (TranscriptFile, error) {
	if f.open != nil {
		return nil, f.open
	}
	file, err := f.OSFiles.OpenFile(path, flag, mode)
	if err != nil {
		return nil, err
	}
	return &faultFile{TranscriptFile: file, f: f}, nil
}
func (f *faultFiles) Stat(path string) (fs.FileInfo, error) {
	if f.stat != nil {
		return nil, f.stat
	}
	return os.Stat(path)
}
func (f *faultFiles) MkdirAll(path string, mode fs.FileMode) error {
	if f.mkdir != nil {
		return f.mkdir
	}
	return os.MkdirAll(path, mode)
}
func (f *faultFiles) ReadFile(path string) ([]byte, error) {
	if f.read != nil {
		return nil, f.read
	}
	return os.ReadFile(path)
}

type faultFile struct {
	TranscriptFile
	f *faultFiles
}

func (f *faultFile) Stat() (fs.FileInfo, error) {
	if f.f.stat != nil {
		return nil, f.f.stat
	}
	return f.TranscriptFile.Stat()
}
func (f *faultFile) Write(data []byte) (int, error) {
	if f.f.write != nil {
		return 0, f.f.write
	}
	if f.f.zero {
		return 0, nil
	}
	if len(data) > 7 {
		return f.TranscriptFile.Write(data[:7])
	}
	return f.TranscriptFile.Write(data)
}
func (f *faultFile) Close() error {
	err := f.TranscriptFile.Close()
	if f.f.close != nil {
		return f.f.close
	}
	return err
}
func (f *faultFile) Chmod(mode fs.FileMode) error {
	if f.f.chmod != nil {
		return f.f.chmod
	}
	return f.TranscriptFile.Chmod(mode)
}
func readyWriter(t *testing.T, files TranscriptFiles) *Writer {
	t.Helper()
	controller := activeController(t)
	controller.writer.options.Files = files
	return controller.writer
}
func plainMessage(id, content string) *protocol.JSONObject {
	value := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "uuid", Value: quote(id)}, protocol.JSONField{Name: "message", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quote("user")}, protocol.JSONField{Name: "content", Value: quote(content)}))})
	return &value
}
func TestWriterNativeLifecycleAndDescriptorReplacement(t *testing.T) {
	files := &faultFiles{}
	writer := readyWriter(t, files)
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("queue-operation")})
	if err := writer.Append(&entry); err != nil {
		t.Fatal(err)
	}
	if writer.path != "" || len(writer.pending) != 1 {
		t.Fatal("metadata materialized transcript")
	}
	u := plainMessage("u", "first")
	if _, err := writer.Record([]*protocol.JSONObject{u}, quote("prompt")); err != nil {
		t.Fatal(err)
	}
	path := writer.path
	queued, _ := os.ReadFile(path)
	if len(queued) != 0 {
		t.Fatal("queued writes forced before flush")
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "queue-operation") {
		t.Fatal("startup queue not consumed")
	}
	// Replacing the path after enqueue must protect the replacement and discard
	// UUIDs without recreating deleted data. The descriptor identities are real.
	v := plainMessage("v", "new")
	if _, err := writer.Record([]*protocol.JSONObject{u, v}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	writePrivate(t, path, "sentinel\n")
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "sentinel\n" {
		t.Fatal("replaced transcript written")
	}
	if !writer.excluded[string(quote("v"))] {
		t.Fatal("discarded UUID can backfill")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Record([]*protocol.JSONObject{u, v}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("deleted transcript revived")
	}
	writer.Reset()
	if writer.path != "" || writer.pending != nil {
		t.Fatal("pointer reset incomplete")
	}
}
func TestWriterFaultOwnership(t *testing.T) {
	for _, name := range []string{"mkdir", "open", "stat", "close"} {
		t.Run("prepare-"+name, func(t *testing.T) {
			files := &faultFiles{}
			writer := readyWriter(t, files)
			switch name {
			case "mkdir":
				files.mkdir = syscall.EACCES
			case "open":
				files.open = syscall.EACCES
			case "stat":
				files.stat = syscall.EIO
			case "close":
				files.close = syscall.EIO
			}
			if _, err := writer.Record([]*protocol.JSONObject{plainMessage("u", "q")}, nil); err == nil {
				t.Fatal("native materialize fault disappeared")
			}
		})
	}
	for _, name := range []string{"open", "stat", "write", "zero", "close", "chmod"} {
		t.Run("drain-"+name, func(t *testing.T) {
			files := &faultFiles{}
			writer := readyWriter(t, files)
			if _, err := writer.Record([]*protocol.JSONObject{plainMessage("u", "q")}, nil); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "open":
				files.open = syscall.EACCES
			case "stat":
				files.stat = syscall.EIO
			case "write":
				files.write = syscall.EIO
			case "zero":
				files.zero = true
			case "close":
				files.close = syscall.EIO
			case "chmod":
				files.chmod = syscall.EPERM
			}
			var err error
			if name == "chmod" {
				err = writer.RefreshMetadata()
			} else {
				err = writer.Flush()
			}
			if err == nil {
				t.Fatal("native drain/metadata fault disappeared")
			}
			if name == "zero" && !errors.Is(err, io.ErrShortWrite) {
				t.Fatal(err)
			}
		})
	}
}
func TestWriterPolicySwitchDoesNotBackfill(t *testing.T) {
	writer := readyWriter(t, OSFiles{})
	writer.options.SessionPersistenceDisabled = true
	u := plainMessage("excluded", "q")
	if _, err := writer.Record([]*protocol.JSONObject{u}, nil); err != nil {
		t.Fatal(err)
	}
	writer.options.SessionPersistenceDisabled = false
	if _, err := writer.Record([]*protocol.JSONObject{u, plainMessage("new", "next")}, nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(writer.path)
	if strings.Contains(string(data), `"uuid":"excluded"`) {
		t.Fatal("policy reenabled resurrected excluded history")
	}
	if err := writer.Append(nil); err == nil {
		t.Fatal("nil entry accepted")
	}
	writer.options.SessionPersistenceDisabled = true
	if err := writer.Append(plainMessage("skip", "s")); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentAppendFlushUsesSameWriter(t *testing.T) {
	writer := readyWriter(t, OSFiles{})
	if _, err := writer.Record([]*protocol.JSONObject{plainMessage("initial", "q")}, nil); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("observed")}, protocol.JSONField{Name: "index", Value: raw(index)})
			if err := writer.Append(&entry); err != nil {
				t.Error(err)
			}
			if err := writer.Flush(); err != nil {
				t.Error(err)
			}
		}(index)
	}
	wait.Wait()
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(writer.path)
	if strings.Count(string(data), `"type":"observed"`) != 20 {
		t.Fatal("concurrent queued writes lost")
	}
}
func TestMetadataSnapshotsAndGitCapability(t *testing.T) {
	writer := readyWriter(t, OSFiles{})
	writer.RestoreMetadata(object([]byte(`{"customTitle":"title","tag":"tag","agentName":"agent","agentColor":"pink","agentSetting":"fixture","mode":"normal","worktreeSession":null,"prNumber":0,"prUrl":"url","prRepository":"repo"}`)))
	if _, err := writer.Record([]*protocol.JSONObject{plainMessage("u", "q")}, nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := writer.RefreshMetadata(); err != nil {
		t.Fatal(err)
	}
	snapshot := writer.Snapshot()
	snapshot.Set("metadata", raw(nil))
	if !object(writer.Snapshot().Get("metadata")).IsObject() {
		t.Fatal("observer changed metadata")
	}
	writer.RestoreMetadata(object([]byte(`{"customTitle":"later","tag":"","worktreeSession":null}`)))
	if text(writer.metadata.Get("customTitle")) != "title" || truthy(writer.metadata.Get("tag")) {
		t.Fatal("metadata priority/clear mismatch")
	}
	home := t.TempDir()
	if _, err := NoRepositoryBranch(home, nil); err == nil {
		t.Fatal("missing Git observation accepted")
	}
	writePrivate(t, filepath.Join(home, ".git"), "gitdir: private")
	if _, err := NoRepositoryBranch(home, os.Stat); err == nil {
		t.Fatal("active worktree rendered as HEAD")
	}
	if value, err := NoRepositoryBranch(home, func(string) (fs.FileInfo, error) { return nil, syscall.EACCES }); err != nil || value != "HEAD" {
		t.Fatal("actual Source stat-catch profile lost")
	}
	stop := RealSchedule(time.Hour, func() { t.Error("cancelled scheduler fired") })
	stop()
}

func TestAdoptedWriterCapturesExistingDescriptorAndDoesNotCreate(t *testing.T) {
	for _, name := range []string{"existing", "missing", "open", "stat", "close", "directory"} {
		t.Run(name, func(t *testing.T) {
			files := &faultFiles{}
			writer := readyWriter(t, files)
			writer.AdoptSessionFile()
			if name != "missing" {
				if name == "directory" {
					if err := os.MkdirAll(writer.path, 0700); err != nil {
						t.Fatal(err)
					}
				} else {
					writePrivate(t, writer.path, "sentinel\n")
				}
			}
			switch name {
			case "open":
				files.open = syscall.EACCES
			case "stat":
				files.stat = syscall.EIO
			case "close":
				files.close = syscall.EIO
			}
			err := writer.Append(plainMessage("u", "q"))
			if name == "missing" || name == "existing" {
				if err != nil {
					t.Fatal(err)
				}
				if err = writer.Flush(); err != nil {
					t.Fatal(err)
				}
				if name == "missing" {
					if _, err = os.Stat(writer.path); !errors.Is(err, fs.ErrNotExist) {
						t.Fatal("adopted append created transcript")
					}
				} else {
					data, _ := os.ReadFile(writer.path)
					if !strings.HasPrefix(string(data), "sentinel\n") {
						t.Fatal("adopted writer rewrote data")
					}
				}
			} else if err == nil {
				t.Fatal("actual descriptor failure hidden")
			}
		})
	}
	writer := readyWriter(t, OSFiles{})
	writer.AdoptSessionFile()
	writePrivate(t, writer.path, "before\n")
	if err := writer.Append(plainMessage("same", "q")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(plainMessage("same", "duplicate")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(&[]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "uuid", Value: raw(3)})}[0]); err == nil {
		t.Fatal("non-string identity accepted")
	}
	if err := os.Remove(writer.path); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(writer.path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("flush resurrected deleted adopted transcript")
	}
}

func TestOriginalBatchPreflightKeepsSDKQueueUntouched(t *testing.T) {
	controller := activeController(t)
	inputs := []protocol.JSONObject{object([]byte(`{"type":"user","uuid":"first","message":{"content":"first"}}`)), object([]byte(`{"type":"user","uuid":"second","message":{"content":"second"},"priority":"now"}`))}
	if _, err := controller.AdmitOriginalBatch(inputs); err == nil {
		t.Fatal("active-turn streaming pretended available")
	}
	if len(controller.QueueEvents()) != 0 || len(controller.History()) != 0 || len(controller.writer.pending) != 0 {
		t.Fatal("original finite preflight enqueued before rejecting")
	}
	var failure *CapabilityError
	if _, err := controller.AdmitSDK(object([]byte(`{"type":"user","file_attachments":[],"message":{"content":"plain"}}`))); !errors.As(err, &failure) || failure.Error() == "" {
		t.Fatal("unknown SDK/file capability not refused", err)
	}
	if _, err := controller.AdmitSDK(inputs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.AdmitSDK(inputs[1]); err == nil {
		t.Fatal("second streamed novel prompt silently admitted")
	}
}
