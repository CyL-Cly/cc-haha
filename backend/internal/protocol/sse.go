package protocol

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// SSEEvent contains an SSE dispatch. Data lines are joined with a newline.
// ID and Retry are transport metadata, not an Anthropic message identity.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
	Retry *int
}

// SSEDecoder supports LF, CRLF and CR split across reads, comments, multiline
// data, and events larger than bufio.Scanner's default limit. Like the Anthropic
// SDK, it replacement-decodes UTF-8 and removes one BOM from each logical line.
// Only an actual blank line dispatches data; EOF discards a pending frame.
type SSEDecoder struct {
	reader       *bufio.Reader
	eof          bool
	lastID       string
	retry        *int
	transportErr error
}

func NewSSEDecoder(reader io.Reader) *SSEDecoder {
	return &SSEDecoder{reader: bufio.NewReader(reader)}
}

func (decoder *SSEDecoder) Next() (SSEEvent, error) {
	if decoder.eof {
		if decoder.transportErr != nil {
			return SSEEvent{}, decoder.transportErr
		}
		return SSEEvent{}, io.EOF
	}
	var name string
	var data []string
	for {
		line, err := decoder.readLine()
		if err != nil {
			decoder.eof = true
			if err != io.EOF {
				decoder.transportErr = err
			}
			// SDK LineDecoder.flush terminates an unterminated line but does
			// not add an empty separator. Neither EOF nor a transport error
			// turns the pending frame into a dispatched provider event.
			return SSEEvent{}, err
		}
		line = strings.TrimPrefix(string(DecodeJavaScriptUTF8([]byte(line))), "\ufeff")
		if line == "" {
			if len(data) > 0 {
				return SSEEvent{Event: name, Data: strings.Join(data, "\n"), ID: decoder.lastID, Retry: cloneInt(decoder.retry)}, nil
			}
			name = ""
		} else if !strings.HasPrefix(line, ":") {
			field, value, found := strings.Cut(line, ":")
			if !found {
				value = ""
			}
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				name = value
			case "data":
				data = append(data, value)
			case "id":
				if !strings.ContainsRune(value, '\x00') {
					decoder.lastID = value
				}
			case "retry":
				if decimalDigits(value) {
					if retry, parseErr := strconv.Atoi(value); parseErr == nil {
						decoder.retry = &retry
					}
				}
			}
		}
	}
}

func (decoder *SSEDecoder) readLine() (string, error) {
	var line strings.Builder
	for {
		b, err := decoder.reader.ReadByte()
		if err != nil {
			return line.String(), err
		}
		if b == '\n' {
			return line.String(), nil
		}
		if b == '\r' {
			if next, _ := decoder.reader.Peek(1); len(next) == 1 && next[0] == '\n' {
				_, _ = decoder.reader.ReadByte()
			}
			return line.String(), nil
		}
		line.WriteByte(b)
	}
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
