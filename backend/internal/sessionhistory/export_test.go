package sessionhistory

import (
	"encoding/json"
	"testing"
)

func TestActualMetadataMapRetainsMissingNullUTF16AndClonesObservers(t *testing.T) {
	loader, _ := nativeLoader(t)
	path := writeHistory(t, `{"type":"pr-link","sessionId":"\ud800","prNumber":null,"prUrl":"\udc00"}`+"\n")
	transcript, err := loader.LoadTranscriptFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if value, exists := transcript.MetadataValue("prRepositories", json.RawMessage(`"\ud800"`)); !exists || value != nil {
		t.Fatal("present undefined collapsed into null")
	}
	if value, exists := transcript.MetadataValue("prNumbers", json.RawMessage(`"\ud800"`)); !exists || string(value) != "null" {
		t.Fatal("explicit JSON null lost")
	}
	if value, exists := transcript.MetadataValue("prUrls", json.RawMessage(`"\ud800"`)); !exists || string(value) != `"\udc00"` {
		t.Fatal("UTF-16 key/value changed")
	} else {
		value[1] = 'x'
	}
	if value, exists := transcript.MetadataValue("prUrls", json.RawMessage(`"\ud800"`)); !exists || string(value) != `"\udc00"` {
		t.Fatal("observer mutated raw map")
	}
	if _, exists := transcript.MetadataValue("prUrls", json.RawMessage(`"\ud801"`)); exists {
		t.Fatal("distinct UTF-16 identities conflated")
	}
	if _, exists := transcript.MetadataValue("unknown", json.RawMessage(`"\ud800"`)); exists {
		t.Fatal("unrecognized map exists")
	}
	if _, exists := (*Transcript)(nil).MetadataValue("prUrls", nil); exists {
		t.Fatal("nil transcript map exists")
	}
	objectPath := writeHistory(t, `{"type":"custom-title","sessionId":{"original":true},"customTitle":"must-not-match"}`+"\n"+`{"type":"tag","sessionId":["original"],"tag":"must-not-match"}`+"\n")
	objects, err := loader.LoadTranscriptFile(objectPath, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ name, raw string }{{"customTitles", `{"original":true}`}, {"tags", `["original"]`}} {
		if _, exists := objects.MetadataValue(row.name, []byte(row.raw)); exists {
			t.Fatal("new raw object observer acquired stored object reference identity")
		}
	}
}
