package sessionhistory

import (
	"encoding/json"
	"os"
	"testing"
)

func TestMessageSetUsesCapturedActualMapKeys(t *testing.T) {
	loader, _ := nativeLoader(t)
	path := writeHistory(t, `{"type":"user","uuid":"first"}`+"\n"+`{"type":"user","uuid":null}`+"\n"+`{"type":"user"}`+"\n"+`{"type":"user","uuid":{"k":1}}`+"\n"+`{"type":"user","uuid":false}`+"\n"+`{"type":"user","uuid":"\ud800"}`+"\n"+`{"type":"user","uuid":-0}`+"\n"+`{"type":"user","uuid":0}`+"\n")
	value, e := loader.LoadTranscriptFile(path, false)
	if e != nil {
		t.Fatal(e)
	}
	set := value.MessageSet()
	if set.Len() != 7 {
		t.Fatal(set.Len())
	}
	for _, raw := range []json.RawMessage{nil, []byte("null"), []byte(`"first"`), []byte("false"), []byte(`"\ud800"`), []byte("-0"), []byte("0")} {
		if !set.Has(raw) {
			t.Fatal("lost primitive Map key", string(raw))
		}
	}
	if set.Has([]byte(`{"k":1}`)) {
		t.Fatal("fresh object matched parsed object identity")
	}
	entry := value.messages[key([]byte(`"first"`), -1)]
	entry.value.Set("uuid", []byte(`"changed"`))
	value.messages[entry.uuid] = entry
	if value.MessageSet().Has([]byte(`"changed"`)) || !value.MessageSet().Has([]byte(`"first"`)) {
		t.Fatal("Map key was reconstructed from mutable value")
	}
	keys := set.Keys()
	if string(keys[0].Raw()) != `"first"` {
		t.Fatal(keys[0].Raw())
	}
	raw := keys[0].Raw()
	raw[0] = '!'
	keys[0].raw[0] = '!'
	if string(set.Keys()[0].Raw()) != `"first"` {
		t.Fatal("observer mutated cache bytes")
	}
	delete(value.messages, key([]byte(`"first"`), -1))
	if value.MessageSet().Len() != 6 {
		t.Fatal("deleted message remained in Set")
	}
}

func TestReadContinuationRejectsForeignAndInvalidReplies(t *testing.T) {
	loader, _ := nativeLoader(t)
	if _, e := loader.BeginRead(string([]byte{0xff}), false); e == nil {
		t.Fatal("invalid UTF8 path accepted")
	}
	if _, e := loader.FinishRead(nil); e == nil {
		t.Fatal("nil read accepted")
	}
	reader, e := loader.BeginRead("private", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = loader.FinishRead(reader); e == nil {
		t.Fatal("unfinished read accepted")
	}
	other, _ := nativeLoader(t)
	if _, e = other.FinishRead(reader); e == nil {
		t.Fatal("foreign read accepted")
	}
	if e = reader.Complete(PhysicalReadResult{Size: SkipPrecompactThreshold + 1}); e != nil {
		t.Fatal(e)
	}
	if e = reader.Complete(PhysicalReadResult{}); e != nil {
		t.Fatal(e)
	}
	if e = reader.Complete(PhysicalReadResult{Bytes: make([]byte, SkipPrecompactThreshold+2)}); e == nil {
		t.Fatal("oversized physical read accepted")
	}
	reader, e = loader.BeginRead("private", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = reader.Complete(PhysicalReadResult{Error: os.ErrNotExist}); e != nil {
		t.Fatal(e)
	}
	if e = reader.Complete(PhysicalReadResult{}); e == nil {
		t.Fatal("completed read accepted another reply")
	}
	value, e := loader.FinishRead(reader)
	if e != nil || value.MessageSet().Len() != 0 {
		t.Fatal(value, e)
	}
}
