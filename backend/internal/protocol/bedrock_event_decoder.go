package protocol

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// BedrockEventDecoder changes framing only. One original model response and
// owner demand iterator still own parsing, cancellation and physical reads.
type BedrockEventDecoder struct {
	reader                io.Reader
	buffer                []byte
	readError, errorValue error
	frames                []SDKSSEEvent
	emptyReads            int
}

func NewBedrockEventDecoder(reader io.Reader) *BedrockEventDecoder {
	return &BedrockEventDecoder{reader: reader}
}
func (d *BedrockEventDecoder) Next() (SDKSSEEvent, error) {
	for {
		if frame, ok := d.PollFrame(); ok {
			return frame, nil
		}
		chunk, ready, err := d.PollChunk()
		if err != nil {
			return SDKSSEEvent{}, err
		}
		if ready {
			d.DecodeChunk(chunk)
			continue
		}
		var bytes [4096]byte
		n, err := d.reader.Read(bytes[:])
		d.AcceptRead(bytes[:n], err)
	}
}
func (d *BedrockEventDecoder) PollFrame() (SDKSSEEvent, bool) {
	if len(d.frames) == 0 {
		return SDKSSEEvent{}, false
	}
	frame := d.frames[0]
	d.frames = d.frames[1:]
	return frame, true
}
func (d *BedrockEventDecoder) PollChunk() ([]byte, bool, error) {
	if d.errorValue != nil {
		return nil, false, d.errorValue
	}
	if len(d.buffer) >= 4 {
		length := int(binary.BigEndian.Uint32(d.buffer))
		if length < 16 {
			return nil, false, errors.New("Provided message too short to accommodate event stream message overhead")
		}
		if len(d.buffer) >= length {
			chunk := d.buffer[:length]
			d.buffer = d.buffer[length:]
			return chunk, true, nil
		}
	}
	if d.readError != nil {
		if d.readError == io.EOF && len(d.buffer) >= 4 {
			return nil, false, errors.New("Truncated event message received.")
		}
		return nil, false, d.readError
	}
	return nil, false, nil
}
func (d *BedrockEventDecoder) AcceptRead(value []byte, err error) {
	d.buffer = append(d.buffer, value...)
	d.readError = err
	if len(value) == 0 && err == nil {
		d.emptyReads++
		if d.emptyReads >= 100 {
			d.readError = io.ErrNoProgress
		}
	} else {
		d.emptyReads = 0
	}
}
func (d *BedrockEventDecoder) FlushLines() {}
func (d *BedrockEventDecoder) DecodeChunk(chunk []byte) {
	frame, visible, err := DecodeBedrockMessage(chunk)
	if err != nil {
		d.errorValue = err
		return
	}
	if visible {
		d.frames = append(d.frames, frame)
	}
}

type BedrockStreamError struct{ Code, Message string }

func (e *BedrockStreamError) Error() string { return e.Message }
func (e *BedrockStreamError) Name() string  { return e.Code }

func DecodeBedrockMessage(message []byte) (SDKSSEEvent, bool, error) {
	if len(message) < 16 {
		return SDKSSEEvent{}, false, errors.New("Provided message too short to accommodate event stream message overhead")
	}
	length := int(binary.BigEndian.Uint32(message))
	if length != len(message) {
		return SDKSSEEvent{}, false, errors.New("Reported message length does not match received message length")
	}
	headersLength := int(binary.BigEndian.Uint32(message[4:8]))
	if headersLength > length-16 {
		return SDKSSEEvent{}, false, errors.New("event stream headers exceed message")
	}
	expected := binary.BigEndian.Uint32(message[8:12])
	actual := crc32.ChecksumIEEE(message[:8])
	if expected != actual {
		return SDKSSEEvent{}, false, fmt.Errorf("The prelude checksum specified in the message (%d) does not match the calculated CRC32 checksum (%d)", expected, actual)
	}
	expected = binary.BigEndian.Uint32(message[length-4:])
	actual = crc32.ChecksumIEEE(message[:length-4])
	if expected != actual {
		return SDKSSEEvent{}, false, fmt.Errorf("The message checksum (%d) did not match the expected value of %d", actual, expected)
	}
	headers, err := bedrockHeaders(message[12 : 12+headersLength])
	if err != nil {
		return SDKSSEEvent{}, false, err
	}
	body := message[12+headersLength : length-4]
	kind := headers[":message-type"]
	if kind == "error" {
		value := headers[":error-message"]
		if value == "" {
			value = "UnknownError"
		}
		return SDKSSEEvent{}, false, &BedrockStreamError{headers[":error-code"], value}
	}
	if kind == "exception" {
		var data map[string]json.RawMessage
		_ = json.Unmarshal(body, &data)
		var message string
		_ = json.Unmarshal(data["message"], &message)
		if message == "" {
			message = string(DecodeJavaScriptUTF8(body))
		}
		return SDKSSEEvent{}, false, &BedrockStreamError{headers[":exception-type"], message}
	}
	if kind != "event" {
		return SDKSSEEvent{}, false, fmt.Errorf("Unrecognizable event type: %s", headers[":event-type"])
	}
	switch headers[":event-type"] {
	case "chunk":
		var data map[string]json.RawMessage
		if err = json.Unmarshal(body, &data); err != nil {
			return SDKSSEEvent{}, false, err
		}
		var encoded string
		if json.Unmarshal(data["bytes"], &encoded) != nil || encoded == "" {
			return SDKSSEEvent{}, false, nil
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return SDKSSEEvent{}, false, err
		}
		return SDKSSEEvent{Event: "chunk", Data: string(DecodeJavaScriptUTF8(decoded)), Raw: []string{}}, true, nil
	case "internalServerException":
		return SDKSSEEvent{Event: "error", Data: "InternalServerException", Raw: []string{}}, true, nil
	case "modelStreamErrorException":
		return SDKSSEEvent{Event: "error", Data: "ModelStreamErrorException", Raw: []string{}}, true, nil
	case "validationException":
		return SDKSSEEvent{Event: "error", Data: "ValidationException", Raw: []string{}}, true, nil
	case "throttlingException":
		return SDKSSEEvent{Event: "error", Data: "ThrottlingException", Raw: []string{}}, true, nil
	default:
		return SDKSSEEvent{}, false, nil
	}
}
func bedrockHeaders(raw []byte) (map[string]string, error) {
	result := map[string]string{}
	for len(raw) > 0 {
		size := int(raw[0])
		raw = raw[1:]
		if len(raw) < size+1 {
			return nil, io.ErrUnexpectedEOF
		}
		name := string(DecodeJavaScriptUTF8(raw[:size]))
		kind := raw[size]
		raw = raw[size+1:]
		length := 0
		switch kind {
		case 0, 1:
			length = 0
		case 2:
			length = 1
		case 3:
			length = 2
		case 4:
			length = 4
		case 5, 8:
			length = 8
		case 9:
			length = 16
		case 6, 7:
			if len(raw) < 2 {
				return nil, io.ErrUnexpectedEOF
			}
			length = int(binary.BigEndian.Uint16(raw[:2]))
			raw = raw[2:]
		default:
			return nil, fmt.Errorf("Unrecognized header type tag: %d", kind)
		}
		if len(raw) < length {
			return nil, io.ErrUnexpectedEOF
		}
		if kind == 7 {
			result[name] = string(DecodeJavaScriptUTF8(raw[:length]))
		}
		raw = raw[length:]
	}
	return result, nil
}
