// Package protocol implements the language-independent transports used by the
// agent worker. It does not load settings, call providers, or execute tools.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// EncodeNDJSON produces one complete JSON line. Like ndjsonSafeStringify.ts,
// it escapes JavaScript's U+2028 and U+2029 line terminators. HTML characters
// remain literal, matching JSON.stringify rather than Go's default encoder.
func EncodeNDJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	line := bytes.ReplaceAll(buffer.Bytes(), []byte("\u2028"), []byte(`\u2028`))
	line = bytes.ReplaceAll(line, []byte("\u2029"), []byte(`\u2029`))
	return line, nil
}

// NDJSONEncoder serializes concurrent producers without interleaving lines.
// An encoding error writes no bytes. A writer error is sticky: after a partial
// line, sending another record would corrupt the one-message-per-line stream.
type NDJSONEncoder struct {
	writer io.Writer
	mu     sync.Mutex
	err    error
}

func NewNDJSONEncoder(writer io.Writer) *NDJSONEncoder {
	return &NDJSONEncoder{writer: writer}
}

func (encoder *NDJSONEncoder) Encode(value any) error {
	line, err := EncodeNDJSON(value)
	if err != nil {
		return err
	}
	encoder.mu.Lock()
	defer encoder.mu.Unlock()
	if encoder.err != nil {
		return encoder.err
	}
	for len(line) > 0 {
		n, err := encoder.writer.Write(line)
		if n < 0 || n > len(line) {
			err = errors.New("NDJSON writer returned an invalid byte count")
			n = 0
		}
		line = line[n:]
		if err != nil {
			encoder.err = err
			return err
		}
		if n == 0 {
			encoder.err = io.ErrShortWrite
			return encoder.err
		}
	}
	return nil
}
