package resumestate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

func TestOuterAttachmentFilterPreservesDirectReducerWarnCountAndOrder(t *testing.T) {
	home := t.TempDir()
	options, events := testOptions(t, home, OSFiles{})
	raw := []json.RawMessage{plain("u", "original"), []byte(`{"type":"attachment"}`), []byte(`{"type":"attachment","attachment":{"type":"skill_listing","content":"listed"}}`), []byte(`{"type":"attachment","attachment":{"type":"invoked_skills","skills":[null]}}`)}
	history, err := sessionresume.ParseMessages(raw)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := options.Reducer.FilterMalformedAttachments(history)
	if err != nil || len(retained) != 2 || retained[0] != history[0] || retained[1] != history[2] {
		t.Fatal("outer filter changed identity/order", err)
	}
	result, err := options.Reducer.Deserialize(retained)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.diagnostics) != 1 || !strings.Contains(string(events.diagnostics[0]), "dropped 2 attachment entries") {
		t.Fatal("outer plus inner emitted duplicate or missing warning")
	}
	directOptions, directEvents := testOptions(t, home, OSFiles{})
	directHistory, err := sessionresume.ParseMessages(raw)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := directOptions.Reducer.Deserialize(directHistory)
	if err != nil || string(encode(result)) != string(encode(direct)) || string(encode(events.diagnostics)) != string(encode(directEvents.diagnostics)) {
		t.Fatal("narrow shared split changed direct reducer", err)
	}
}

func TestExportedChainRetainsRawAndDoesNotMutateTranscript(t *testing.T) {
	home := t.TempDir()
	options, _ := testOptions(t, home, OSFiles{})
	path := filepath.Join(home, "session.jsonl")
	writePrivate(t, path, `{"parentUuid":null,"isSidechain":false,"type":"user","uuid":"u","timestamp":"2025-01-01T00:00:00Z","sessionId":"s","future":{"z":1,"a":"\ud800"},"message":{"role":"user","content":"q"}}`+"\n")
	transcript, err := options.Loader.LoadTranscriptFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, stripped, err := options.Loader.BuildConversationChain(transcript, []byte(`"u"`))
	if err != nil || len(raw) != 1 || len(stripped) != 1 {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw[0]), `"parentUuid":null`) || !strings.Contains(string(raw[0]), `"isSidechain":false`) || strings.Contains(string(stripped[0]), `"parentUuid"`) || strings.Contains(string(stripped[0]), `"isSidechain"`) || !strings.Contains(string(stripped[0]), `"future":{"z":1,"a":"\ud800"}`) {
		t.Fatal("shared chain removed or reordered persistence/future fields")
	}
	again, _, err := options.Loader.BuildConversationChain(transcript, []byte(`"u"`))
	if err != nil || string(again[0]) != string(raw[0]) {
		t.Fatal("stripped projection mutated the original transcript")
	}
	if _, _, err := options.Loader.BuildConversationChain(nil, []byte(`"u"`)); err == nil {
		t.Fatal("nil transcript accepted")
	}
	if _, _, err := options.Loader.BuildConversationChain(transcript, []byte(`"absent"`)); err == nil {
		t.Fatal("unloaded tip accepted")
	}
}
