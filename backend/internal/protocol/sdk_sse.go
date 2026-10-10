package protocol

import (
	"io"
	"strings"
)

// SDKSSEEvent retains the original logical lines used by the pinned SDK's
// JSON-parse diagnostics. Event-only frames are dispatched too. ID and retry
// lines remain diagnostics and have no state in this Messages SDK profile.
type SDKSSEEvent struct {
	Event string
	Data  string
	Raw   []string
}

// SDKSSEDecoder follows SDK 0.80.0's byte chunker, incremental LineDecoder and
// SSEDecoder. In particular, mixed newline patterns cannot dispatch before the
// SDK chunker exposes them, and a failed read never flushes a pending frame.
type SDKSSEDecoder struct {
	reader     io.Reader
	buffer     []byte
	scan       int
	readErr    error
	finished   bool
	emptyReads int
	flushed    bool
	lineBuffer []byte
	carriage   int // index after a pending CR, or zero
	lines      []string
	event      string
	data       []string
	raw        []string
}

func NewSDKSSEDecoder(reader io.Reader) *SDKSSEDecoder {
	return &SDKSSEDecoder{reader: reader}
}

// Next is the compatibility driver over the same nonblocking decoder steps.
// Only this driver reads; owner demand uses PollChunk/AcceptRead directly.
func (decoder *SDKSSEDecoder) Next() (SDKSSEEvent, error) {
	for {
		if frame, ready := decoder.PollFrame(); ready {
			return frame, nil
		}
		chunk, ready, err := decoder.PollChunk()
		if err != nil {
			if err == io.EOF {
				decoder.FlushLines()
				if frame, ready := decoder.PollFrame(); ready {
					return frame, nil
				}
			}
			return SDKSSEEvent{}, err
		}
		if ready {
			decoder.DecodeChunk(chunk)
			continue
		}
		var storage [4096]byte
		count, failure := decoder.reader.Read(storage[:])
		decoder.AcceptRead(storage[:count], failure)
	}
}

// PollFrame performs only the original logical line/frame reducer on owner.
func (decoder *SDKSSEDecoder) PollFrame() (SDKSSEEvent, bool) {
	for len(decoder.lines) > 0 {
		line := decoder.lines[0]
		decoder.lines = decoder.lines[1:]
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if decoder.event == "" && len(decoder.data) == 0 {
				// Comment-only frames keep their diagnostic chunks in the SDK.
				continue
			}
			frame := SDKSSEEvent{Event: decoder.event, Data: strings.Join(decoder.data, "\n"), Raw: decoder.raw}
			decoder.event, decoder.data, decoder.raw = "", nil, nil
			return frame, true
		}
		decoder.raw = append(decoder.raw, line)
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			decoder.event = value
		case "data":
			decoder.data = append(decoder.data, value)
		}
	}
	return SDKSSEEvent{}, false
}

func sdkChunkBoundary(buffer []byte, start int) int {
	for index := start; index+1 < len(buffer); index++ {
		if buffer[index] == '\n' && buffer[index+1] == '\n' || buffer[index] == '\r' && buffer[index+1] == '\r' {
			return index + 2
		}
		if buffer[index] == '\r' && buffer[index+1] == '\n' && index+3 < len(buffer) && buffer[index+2] == '\r' && buffer[index+3] == '\n' {
			return index + 4
		}
	}
	return -1
}

// PollChunk exposes one original SSE byte-chunk without physical I/O. The
// caller's iterator yields it before asking for the retained remainder.
func (decoder *SDKSSEDecoder) PollChunk() ([]byte, bool, error) {
	if decoder.finished {
		if decoder.readErr != nil && decoder.readErr != io.EOF {
			return nil, false, decoder.readErr
		}
		return nil, false, io.EOF
	}
	if end := sdkChunkBoundary(decoder.buffer, decoder.scan); end >= 0 {
		chunk := decoder.buffer[:end]
		decoder.buffer = decoder.buffer[end:]
		decoder.scan = 0
		return chunk, true, nil
	}
	decoder.scan = max(0, len(decoder.buffer)-3)
	if decoder.readErr != nil {
		decoder.finished = true
		if decoder.readErr != io.EOF {
			return nil, false, decoder.readErr
		}
		if len(decoder.buffer) > 0 {
			chunk := decoder.buffer
			decoder.buffer = nil
			return chunk, true, nil
		}
		return nil, false, io.EOF
	}
	return nil, false, nil
}

// AcceptRead copies immutable physical completion bytes into the one parser.
func (decoder *SDKSSEDecoder) AcceptRead(value []byte, failure error) {
	decoder.buffer = append(decoder.buffer, value...)
	decoder.readErr = failure
	if len(value) == 0 && failure == nil {
		decoder.emptyReads++
		if decoder.emptyReads == 100 {
			decoder.readErr = io.ErrNoProgress
		}
	} else {
		decoder.emptyReads = 0
	}
}

func (decoder *SDKSSEDecoder) DecodeChunk(chunk []byte) { decoder.decodeLines(chunk) }

// FlushLines belongs to the SSE generator's post-chunk-EOF continuation.
func (decoder *SDKSSEDecoder) FlushLines() {
	if !decoder.flushed {
		decoder.flushed = true
		if len(decoder.lineBuffer) > 0 {
			decoder.decodeLines([]byte{'\n'})
		}
	}
}

func (decoder *SDKSSEDecoder) decodeLines(chunk []byte) {
	decoder.lineBuffer = append(decoder.lineBuffer, chunk...)
	for {
		preceding := -1
		carriage := false
		for index := decoder.carriage; index < len(decoder.lineBuffer); index++ {
			if decoder.lineBuffer[index] == '\n' || decoder.lineBuffer[index] == '\r' {
				preceding, carriage = index, decoder.lineBuffer[index] == '\r'
				break
			}
		}
		if preceding < 0 {
			return
		}
		end := preceding + 1
		if carriage && decoder.carriage == 0 {
			decoder.carriage = end
			continue
		}
		if decoder.carriage != 0 && (end != decoder.carriage+1 || carriage) {
			decoder.appendLine(decoder.lineBuffer[:decoder.carriage-1])
			decoder.lineBuffer = decoder.lineBuffer[decoder.carriage:]
			decoder.carriage = 0
			continue
		}
		lineEnd := preceding
		if decoder.carriage != 0 {
			lineEnd--
		}
		decoder.appendLine(decoder.lineBuffer[:lineEnd])
		decoder.lineBuffer = decoder.lineBuffer[end:]
		decoder.carriage = 0
	}
}

func (decoder *SDKSSEDecoder) appendLine(line []byte) {
	decoder.lines = append(decoder.lines, strings.TrimPrefix(string(DecodeJavaScriptUTF8(line)), "\ufeff"))
}
