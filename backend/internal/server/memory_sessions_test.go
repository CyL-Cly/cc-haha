package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

func TestRetentionZeroMemoryLaunchReopensSameSIDWithoutTranscriptAndExpiresWithOwner(t *testing.T) {
	var mu sync.Mutex
	opened := []SessionOptions{}
	factory := func(_ context.Context, options SessionOptions, emit Emit) (LiveSession, error) {
		mu.Lock()
		opened = append(opened, options)
		mu.Unlock()
		return &transportSession{emit: emit, received: make(chan json.RawMessage, 4)}, nil
	}
	options := testOptions(t, factory)
	options.WorkDir, _ = filepath.EvalSymlinks(options.WorkDir)
	options.PersistSession = func(context.Context) (bool, error) { return false, nil }
	options.ClassifyRecordedCommand = func(string) (bool, error) { return true, nil }
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	projects := filepath.Join(options.ConfigDir, "projects")
	if _, err := os.Stat(projects); !os.IsNotExist(err) {
		t.Fatalf("retention-zero created project storage: %v", err)
	}
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/summary", "", 404)
	requestJSON(t, host, "GET", "/api/sessions/"+id, "", 404)
	page := requestJSON(t, host, "GET", "/api/sessions/"+id+"/messages?mode=page&cursor=unused", "", 200)
	var actual map[string]any
	if err := json.Unmarshal(page, &actual); err != nil {
		t.Fatal(err)
	}
	if actual["page"].(map[string]any)["sourceVersion"] != "memory" || len(actual["messages"].([]any)) != 0 {
		t.Fatalf("memory history=%s", page)
	}
	var expected map[string]any
	_ = json.Unmarshal([]byte(`{"messages":[],"taskNotifications":[],"page":{"nextCursor":null,"hasMore":false,"historyComplete":true,"sourceVersion":"memory","scannedBytes":0,"omittedOversizedEntries":0}}`), &expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Source memory page mismatch: %s", page)
	}
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/turn-checkpoints", "", 200)
	evidence, err := server.checkpointMessages(context.Background(), id)
	if err != nil || evidence.Complete || len(evidence.Messages) != 0 {
		t.Fatalf("memory evidence=%+v err=%v", evidence, err)
	}
	if _, err = server.live(id); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	initial := opened[0]
	mu.Unlock()
	if initial.ID != id || initial.Resume || initial.WorkDir != options.WorkDir || initial.UpdateLaunchMetadata == nil {
		t.Fatalf("initial=%+v", initial)
	}
	selected := filepath.Join(options.HomeDir, "selected-workspace")
	if err = os.Mkdir(selected, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]any{"workDir": selected, "runtimeProviderId": "private-provider", "runtimeModelId": "private-model", "effortLevel": "high", "permissionMode": "acceptEdits", "future": map[string]any{"nested": []any{nil, 17, true}}})
	if err = initial.UpdateLaunchMetadata(context.Background(), metadata); err != nil {
		t.Fatal(err)
	}
	for i := range metadata {
		metadata[i] = 'x'
	}
	if err = server.stopSessionAndWait(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err = server.live(id); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	reopened := opened[1]
	mu.Unlock()
	var launch map[string]json.RawMessage
	if err = json.Unmarshal(reopened.Metadata, &launch); err != nil {
		t.Fatal(err)
	}
	if reopened.ID != id || reopened.Resume || reopened.WorkDir != selected || reopened.PermissionMode != "acceptEdits" || stringField(launch, "runtimeModelId") != "private-model" || string(launch["future"]) != `{"nested":[null,17,true]}` {
		t.Fatalf("reopened=%+v metadata=%s", reopened, reopened.Metadata)
	}
	if _, err = os.Stat(projects); !os.IsNotExist(err) {
		t.Fatalf("runtime metadata wrote project storage: %v", err)
	}
	list := requestJSON(t, host, "GET", "/api/sessions", "", 200)
	if err = json.Unmarshal(list, &actual); err != nil {
		t.Fatal(err)
	}
	if actual["total"] != float64(0) || len(actual["sessions"].([]any)) != 0 {
		t.Fatalf("memory leaked into disk list: %s", list)
	}
	_, restarted := testHost(t, options)
	requestJSON(t, restarted, "GET", "/api/sessions/"+id+"/messages", "", 404)
	requestJSON(t, host, "DELETE", "/api/sessions/"+id, "", 200)
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/messages", "", 404)
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/turn-checkpoints", "", 404)
	if err = server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = initial.UpdateLaunchMetadata(context.Background(), json.RawMessage(`{"effortLevel":"low"}`)); err == nil {
		t.Fatal("closed/deleted owner accepted a stale runtime mutation")
	}
	server.mu.Lock()
	retained := len(server.knownSessions) + len(server.memoryLaunch) + len(server.privateTitles)
	server.mu.Unlock()
	if retained != 0 {
		t.Fatalf("owner retained %d private records after close", retained)
	}
}

func TestRetentionTogglePreservesPrivateTitleAndKnownSessionAfterPhysicalCleanup(t *testing.T) {
	var persist atomic.Bool
	persist.Store(true)
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		return &transportSession{received: make(chan json.RawMessage, 4)}, nil
	})
	options.WorkDir, _ = filepath.EvalSymlinks(options.WorkDir)
	options.PersistSession = func(context.Context) (bool, error) { return persist.Load(), nil }
	options.ClassifyRecordedCommand = func(string) (bool, error) { return true, nil }
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	path := filepath.Join(options.ConfigDir, "projects", projectpath.Sanitize(options.WorkDir), id+".jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	persist.Store(false)
	requestJSON(t, host, "PATCH", "/api/sessions/"+id, `{"title":"private title"}`, 200)
	if err = server.updateLaunchMetadata(context.Background(), id, json.RawMessage(`{"runtimeProviderId":null,"runtimeModelId":"private-model","future":0}`)); err != nil {
		t.Fatal(err)
	}
	persist.Store(true)
	requestJSON(t, host, "PATCH", "/api/sessions/"+id, `{"title":"private title"}`, 200)
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatalf("private title reached disk: %s err=%v", after, err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		persist.Store(enabled)
		requestJSON(t, host, "GET", "/api/sessions/"+id+"/messages", "", 200)
		requestJSON(t, host, "GET", "/api/sessions/"+id+"/turn-checkpoints", "", 200)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("restoring retention resurrected private transcript: %v", err)
		}
	}
	requestJSON(t, host, "DELETE", "/api/sessions/"+id, "", 200)
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/messages", "", 404)
}

func TestKnownPositiveSessionKeepsIncompleteEvidenceImmediatelyAfterRetentionCleanup(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		return &transportSession{received: make(chan json.RawMessage, 4)}, nil
	})
	options.ClassifyRecordedCommand = func(string) (bool, error) { return true, nil }
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(info.Path); err != nil {
		t.Fatal(err)
	}
	evidence, err := server.checkpointMessages(context.Background(), id)
	if err != nil || evidence.Complete {
		t.Fatalf("deleted known evidence=%+v err=%v", evidence, err)
	}
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/turn-checkpoints", "", 200)
}

func TestMemoryLaunchOverlayRetainsOriginalConversationCountForResume(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	server, _ := testHost(t, options)
	server.rememberMemory(sessionInfo{ID: "original", Metadata: json.RawMessage(`{"runtimeModelId":"selected","transcriptMessageCount":0}`)})
	info := server.overlayMemory(sessionInfo{ID: "original", MessageCount: 0, WorkDir: options.WorkDir, Metadata: json.RawMessage(`{"transcriptMessageCount":3,"originalFuture":false}`)})
	if !server.sessionOptions(info).Resume || stringField(historyRecord(info.Metadata), "runtimeModelId") != "selected" || string(historyRecord(info.Metadata)["originalFuture"]) != "false" {
		t.Fatalf("disk launch evidence was replaced by display count: %s", info.Metadata)
	}
}
