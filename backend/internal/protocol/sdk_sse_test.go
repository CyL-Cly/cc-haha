package protocol

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// The generic SSE decoder discarded this frame before the SDK-specific adapter.
func TestSDKEventOnlyFrameIsNotSilentlyDropped(t *testing.T) {
	decoder := NewSDKSSEDecoder(strings.NewReader("event: message_stop\n\n"))
	event, err := decoder.Next()
	if err == io.EOF || err != nil || event.Event != "message_stop" || event.Data != "" {
		t.Fatalf("SDK event-only frame was discarded: %#v, %v", event, err)
	}
}

func TestSDKRawLinesAndEventDispatchAcrossAllFragmentWidths(t *testing.T) {
	wire := "\ufeff: prior 中文🙂\r\n\r\nid: not-state\r\nretry: 10\r\nevent: completion\r\ndata: {\r\n\ufeffdata:  \"text\":\"🙂\"\r\ndata: }\r\n\r\nevent: message_stop\ndata\n\n"
	expected := []SDKSSEEvent{
		{Event: "completion", Data: "{\n \"text\":\"🙂\"\n}", Raw: []string{": prior 中文🙂", "id: not-state", "retry: 10", "event: completion", "data: {", "data:  \"text\":\"🙂\"", "data: }"}},
		{Event: "message_stop", Data: "", Raw: []string{"event: message_stop", "data"}},
	}
	for step := 1; step <= len(wire); step++ {
		decoder := NewSDKSSEDecoder(&fragmentReader{data: []byte(wire), step: step})
		for index, want := range expected {
			got, err := decoder.Next()
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("fragment=%d frame=%d got=%#v err=%v want=%#v", step, index, got, err, want)
			}
		}
		if _, err := decoder.Next(); err != io.EOF {
			t.Fatal(err)
		}
	}
}

func TestSDKFrameResetEmptyEventAndEOF(t *testing.T) {
	for _, ending := range []string{"", "\n", "\r", "\r\n", "\n\n", "\r\r", "\r\n\r\n", "\n\r", "\r\n\n"} {
		for step := 1; step <= 5; step++ {
			decoder := NewSDKSSEDecoder(&fragmentReader{data: []byte("event: ignored\nevent:\n\n: comment\n\nevent: message_stop" + ending), step: step})
			frame, err := decoder.Next()
			complete := ending == "\n\n" || ending == "\r\r" || ending == "\r\n\r\n" || ending == "\n\r" || ending == "\r\n\n"
			if complete {
				if err != nil || frame.Event != "message_stop" || !reflect.DeepEqual(frame.Raw, []string{"event: ignored", "event:", ": comment", "event: message_stop"}) {
					t.Fatalf("%q: %#v %v", ending, frame, err)
				}
			} else if err != io.EOF {
				t.Fatalf("pending %q dispatched: %#v %v", ending, frame, err)
			}
			if _, err := decoder.Next(); err != io.EOF {
				t.Fatal(err)
			}
		}
	}
}

func TestSDKTransportFailureCannotExposeMixedDelimiterPendingChunk(t *testing.T) {
	cause := errors.New("fixture reset")
	decoder := NewSDKSSEDecoder(&readerThenError{data: []byte("event: completion\ndata: {}\n\revent: message_stop\n"), err: cause})
	if frame, err := decoder.Next(); !errors.Is(err, cause) || frame.Event != "" {
		t.Fatalf("failed chunk dispatched: %#v %v", frame, err)
	}
	if _, err := decoder.Next(); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	decoder = NewSDKSSEDecoder(&readerThenError{data: []byte("event: completion\ndata: {}\n\nevent: message_stop\n"), err: cause})
	if frame, err := decoder.Next(); err != nil || frame.Event != "completion" {
		t.Fatal(frame, err)
	}
	if _, err := decoder.Next(); !errors.Is(err, cause) {
		t.Fatal(err)
	}
}

type sdkEmptyReader struct{}

func (sdkEmptyReader) Read([]byte) (int, error) { return 0, nil }

func TestSDKEmptyReaderFailsWithoutSpinningAndReplacementUTF8(t *testing.T) {
	if _, err := NewSDKSSEDecoder(sdkEmptyReader{}).Next(); err != io.ErrNoProgress {
		t.Fatal(err)
	}
	wire := append([]byte("event: completion\ndata: \""), 0xe2, 0x82, 'x')
	wire = append(wire, []byte("\"\n\n")...)
	frame, err := NewSDKSSEDecoder(&fragmentReader{data: wire, step: 1}).Next()
	if err != nil || frame.Data != "\"\ufffdx\"" {
		t.Fatal(frame, err)
	}
}
