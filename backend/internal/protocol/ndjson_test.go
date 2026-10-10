package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
)

func TestNDJSONEscapesOnlyTransportLineTerminators(t *testing.T) {
	value := map[string]any{
		"type":       "assistant",
		"session_id": "fixture-session",
		"uuid":       "fixture-uuid",
		"message": map[string]any{
			"id": "upstream-id", "text": "中文🙂<>&\n\r\u2028\u2029",
		},
	}
	encoded, err := EncodeNDJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(encoded, []byte{'\n'}) != 1 || encoded[len(encoded)-1] != '\n' {
		t.Fatalf("record is not one complete line: %q", encoded)
	}
	for _, raw := range []string{"\u2028", "\u2029", "\r"} {
		if bytes.Contains(encoded, []byte(raw)) {
			t.Fatalf("unescaped line terminator %q in %q", raw, encoded)
		}
	}
	for _, expected := range []string{`\u2028`, `\u2029`, "中文🙂<>&", `\n`, `\r`} {
		if !bytes.Contains(encoded, []byte(expected)) {
			t.Errorf("missing %q in %q", expected, encoded)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["message"].(map[string]any)["text"] != value["message"].(map[string]any)["text"] {
		t.Fatal("transport encoding changed text")
	}
}

func TestNDJSONPreservesControlPayloadAndLargeJSONNumbers(t *testing.T) {
	value := json.RawMessage(`{"type":"control_response","response":{"subtype":"success","request_id":"permission-1","response":{"behavior":"allow","updatedInput":{"counter":9007199254740993},"unknown_extension":{"enabled":true}}}}`)
	encoded, err := EncodeNDJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSuffix(string(encoded), "\n") != string(value) {
		t.Fatalf("changed raw protocol payload: %s", encoded)
	}
}

func TestNDJSONRawMessageLineTerminatorsAreEscaped(t *testing.T) {
	line, err := EncodeNDJSON(json.RawMessage("{\"text\":\"a\u2028b\u2029c\"}"))
	if err != nil {
		t.Fatal(err)
	}
	if string(line) != "{\"text\":\"a\\u2028b\\u2029c\"}\n" {
		t.Fatalf("unsafe raw JSON: %q", line)
	}
}

func TestNDJSONEncodingErrorWritesNothingAndDoesNotPoisonStream(t *testing.T) {
	var writer bytes.Buffer
	encoder := NewNDJSONEncoder(&writer)
	if err := encoder.Encode(map[string]float64{"notJSON": math.NaN()}); err == nil {
		t.Fatal("expected unsupported JSON number error")
	}
	if writer.Len() != 0 {
		t.Fatal("encoding error emitted a partial line")
	}
	if err := encoder.Encode(map[string]any{"type": "keep_alive"}); err != nil {
		t.Fatal(err)
	}
	if writer.String() != "{\"type\":\"keep_alive\"}\n" {
		t.Fatalf("valid record missing: %q", writer.String())
	}
}

func TestNDJSONConcurrentWritesRemainCompleteRecords(t *testing.T) {
	var writer bytes.Buffer
	encoder := NewNDJSONEncoder(&writer)
	var group sync.WaitGroup
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := encoder.Encode(map[string]int{"index": index}); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	seen := make(map[int]bool)
	for _, line := range strings.Split(strings.TrimSuffix(writer.String(), "\n"), "\n") {
		var value map[string]int
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatalf("interleaved record %q: %v", line, err)
		}
		seen[value["index"]] = true
	}
	if len(seen) != 100 {
		t.Fatalf("lost records: got %d", len(seen))
	}
}

type limitedWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (writer *limitedWriter) Write(value []byte) (int, error) {
	if len(value) > writer.limit {
		value = value[:writer.limit]
	}
	return writer.buffer.Write(value)
}

type failedWriter struct {
	calls int
	err   error
}

func (writer *failedWriter) Write(value []byte) (int, error) {
	writer.calls++
	return min(3, len(value)), writer.err
}

func TestNDJSONShortWritesAndStickyFailure(t *testing.T) {
	writer := &limitedWriter{limit: 2}
	if err := NewNDJSONEncoder(writer).Encode(map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if writer.buffer.String() != "{\"n\":1}\n" {
		t.Fatalf("short writes dropped bytes: %q", writer.buffer.String())
	}
	cause := errors.New("broken fixture pipe")
	failed := &failedWriter{err: cause}
	encoder := NewNDJSONEncoder(failed)
	for index := 0; index < 2; index++ {
		if err := encoder.Encode(map[string]int{"n": index}); !errors.Is(err, cause) {
			t.Fatalf("failure not preserved: %v", err)
		}
	}
	if failed.calls != 1 {
		t.Fatalf("wrote a new record after a partial-line error: %d calls", failed.calls)
	}
	zero := &limitedWriter{limit: 0}
	if err := NewNDJSONEncoder(zero).Encode(1); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero-byte write did not fail: %v", err)
	}
}

type invalidCountWriter struct {
	count int
	calls int
}

func (writer *invalidCountWriter) Write(value []byte) (int, error) {
	writer.calls++
	if writer.count < 0 {
		return writer.count, nil
	}
	return len(value) + writer.count, nil
}

func TestNDJSONBrokenWriterContractFailsWithoutPanicOrNextRecord(t *testing.T) {
	for _, count := range []int{-1, 1} {
		writer := &invalidCountWriter{count: count}
		encoder := NewNDJSONEncoder(writer)
		first := encoder.Encode(map[string]any{"type": "assistant"})
		if first == nil {
			t.Fatal("impossible writer byte count accepted")
		}
		if next := encoder.Encode(map[string]any{"type": "result"}); next != first || writer.calls != 1 {
			t.Fatalf("sent another record after writer corruption: %v, calls=%d", next, writer.calls)
		}
	}
}
