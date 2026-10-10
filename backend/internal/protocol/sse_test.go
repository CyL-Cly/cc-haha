package protocol

import (
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type fragmentReader struct {
	data []byte
	step int
}

func (reader *fragmentReader) Read(value []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	count := min(len(value), reader.step, len(reader.data))
	copy(value, reader.data[:count])
	reader.data = reader.data[count:]
	return count, nil
}

func TestSSEFragmentedUTF8CRLFAndMultiline(t *testing.T) {
	input := "\ufeff: 中文🙂 comment\r\nid: transport-id\r\nretry: 1500\r\nevent: message_start\r\ndata: {\r\ndata:   \"text\": \"中文🙂\"\r\ndata: }\r\n\r\n:heartbeat\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for step := 1; step <= len(input); step++ {
		decoder := NewSSEDecoder(&fragmentReader{data: []byte(input), step: step})
		first, err := decoder.Next()
		if err != nil {
			t.Fatalf("fragment width %d: %v", step, err)
		}
		if first.Event != "message_start" || first.Data != "{\n  \"text\": \"中文🙂\"\n}" || first.ID != "transport-id" || first.Retry == nil || *first.Retry != 1500 {
			t.Fatalf("fragment width %d changed event: %#v", step, first)
		}
		*first.Retry = 7
		second, err := decoder.Next()
		if err != nil || second.Event != "message_stop" || second.ID != "transport-id" || *second.Retry != 1500 {
			t.Fatalf("metadata mutated or next event lost at width %d: %#v, %v", step, second, err)
		}
		if _, err := decoder.Next(); err != io.EOF {
			t.Fatalf("wanted EOF, got %v", err)
		}
	}
}

func TestSSEBareCRAndSDKDropsPendingFrameAtEOF(t *testing.T) {
	decoder := NewSSEDecoder(&fragmentReader{data: []byte("event: first\rdata: one\r\revent: second\rdata: two"), step: 1})
	var events []SSEEvent
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if !reflect.DeepEqual(events, []SSEEvent{{Event: "first", Data: "one"}}) {
		t.Fatalf("unterminated frame became an SDK dispatch: %#v", events)
	}
}

func TestSSEDispatchRequiresActualBlankLine(t *testing.T) {
	for _, ending := range []string{"", "\n", "\r", "\r\n", "\n\n", "\r\r", "\r\n\r\n"} {
		t.Run(strconv.Quote(ending), func(t *testing.T) {
			decoder := NewSSEDecoder(&fragmentReader{data: []byte("event: terminal\ndata: done" + ending), step: 1})
			event, err := decoder.Next()
			complete := ending == "\n\n" || ending == "\r\r" || ending == "\r\n\r\n"
			if complete {
				if err != nil || event.Event != "terminal" || event.Data != "done" {
					t.Fatalf("blank-separated frame lost: %#v, %v", event, err)
				}
			} else if err != io.EOF || event.Data != "" {
				t.Fatalf("EOF dispatched pending frame: %#v, %v", event, err)
			}
			if _, err := decoder.Next(); err != io.EOF {
				t.Fatalf("wanted terminal EOF: %v", err)
			}
		})
	}
}

func TestSSESDKReplacementDecodingAndBOMOnEveryLogicalLine(t *testing.T) {
	input := []byte("\ufeffevent: first\n\ufeffdata: \ufeffinside ")
	input = append(input, 0xe2, 0x82, 'x', 0xf4, 0x90, 0x80, 0x80)
	input = append(input, []byte("\n\ufeffdata: second\n\nevent: second\n\ufeff\ufeffdata: ignored\ndata: last\n\n")...)
	for step := 1; step <= len(input); step++ {
		decoder := NewSSEDecoder(&fragmentReader{data: append([]byte(nil), input...), step: step})
		first, err := decoder.Next()
		if err != nil || first.Event != "first" || first.Data != "\ufeffinside \ufffdx\ufffd\ufffd\ufffd\ufffd\nsecond" {
			t.Fatalf("fragment width %d changed line decoding: %#v, %v", step, first, err)
		}
		second, err := decoder.Next()
		if err != nil || second.Event != "second" || second.Data != "last" {
			t.Fatalf("more than one leading BOM removed: %#v, %v", second, err)
		}
	}
}

func TestSSEFieldRulesAndEmptyData(t *testing.T) {
	input := "event: ignored\n\nid: one\nretry: 100\nunknown: hi\ndata\n\nid: bad\x00id\nretry: +1\ndata:  two\n\nid\nretry: 4x\nretry:\ndata: three\n\n"
	decoder := NewSSEDecoder(strings.NewReader(input))
	for index, expected := range []SSEEvent{
		{Data: "", ID: "one", Retry: intPointer(100)},
		{Data: " two", ID: "one", Retry: intPointer(100)},
		{Data: "three", ID: "", Retry: intPointer(100)},
	} {
		actual, err := decoder.Next()
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("event %d: got %#v (%v), want %#v", index, actual, err, expected)
		}
	}
}

func TestSSEAcceptsLargeEventsWithoutScannerTruncation(t *testing.T) {
	data := strings.Repeat("a", 256*1024)
	event, err := NewSSEDecoder(strings.NewReader("data: " + data + "\n\n")).Next()
	if err != nil || event.Data != data {
		t.Fatalf("large event truncated: %d bytes, %v", len(event.Data), err)
	}
}

type readerThenError struct {
	data []byte
	err  error
}

func (reader *readerThenError) Read(value []byte) (int, error) {
	if len(reader.data) > 0 {
		count := copy(value, reader.data)
		reader.data = reader.data[count:]
		return count, nil
	}
	return 0, reader.err
}

func TestSSETransportErrorDoesNotDispatchIncompleteFrame(t *testing.T) {
	cause := errors.New("fixture connection reset")
	decoder := NewSSEDecoder(&readerThenError{data: []byte("data: complete\n\ndata: incomplete"), err: cause})
	first, err := decoder.Next()
	if err != nil || first.Data != "complete" {
		t.Fatalf("lost completed frame: %#v, %v", first, err)
	}
	second, err := decoder.Next()
	if !errors.Is(err, cause) || second.Data != "" {
		t.Fatalf("partial frame accepted after transport failure: %#v, %v", second, err)
	}
	if next, err := decoder.Next(); !errors.Is(err, cause) || next.Data != "" {
		t.Fatalf("read after transport failure manufactured a new event: %#v, %v", next, err)
	}
}

func intPointer(value int) *int { return &value }
