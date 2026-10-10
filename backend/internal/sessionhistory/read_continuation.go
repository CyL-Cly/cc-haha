package sessionhistory

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"io"
	"unicode/utf8"
)

type ReadOperation uint8

const (
	ReadStat ReadOperation = iota
	ReadWhole
	ReadOpen
	ReadAt
	ReadClose
	ReadComplete
)

type ReadRequest struct {
	Operation ReadOperation
	Path      string
	Offset    int64
	Length    int
}

// PhysicalReadResult carries only immutable bytes, size and error. Parser
// state and file descriptors remain separately owned by the caller.
type PhysicalReadResult struct {
	Bytes []byte
	Size  int64
	Error error
}

// PhysicalIO is captured before dispatch; workers use only these external
// capabilities and never the loader, scanner, transcript or mutable messages.
type PhysicalIO struct {
	Files    sessionstore.FileSystem
	Stat     func(string) (int64, error)
	ReadFile func(string) ([]byte, error)
}

func (loader *Loader) PhysicalIO() PhysicalIO {
	return PhysicalIO{loader.options.Files, loader.options.Stat, loader.options.ReadFile}
}

type ReadContinuation struct {
	loader         *Loader
	path           string
	operation      ReadOperation
	keep, disabled bool
	mode           int
	size, offset   int64
	large          largeScanner
	meta           metadataScanner
	data, metadata []byte
	err            error
}

func (loader *Loader) BeginRead(path string, keepAllLeaves bool) (*ReadContinuation, error) {
	if !utf8.ValidString(path) {
		return nil, sessionprojection.ErrUnsupportedPath
	}
	reader := &ReadContinuation{loader: loader, path: path, keep: keepAllLeaves, disabled: loader.options.DisablePrecompactSkip()}
	reader.operation = ReadStat
	if reader.disabled {
		reader.operation = ReadWhole
	}
	return reader, nil
}

func (reader *ReadContinuation) Request() ReadRequest {
	length := 0
	if reader.operation == ReadAt {
		chunk := int64(1024 * 1024)
		if reader.mode == 1 {
			chunk = 64 * 1024
		}
		length = int(min(chunk, reader.size-reader.offset))
	}
	return ReadRequest{reader.operation, reader.path, reader.offset, length}
}

// Complete runs scan work on the caller's owner. It never starts physical I/O.
func (reader *ReadContinuation) Complete(result PhysicalReadResult) error {
	if reader.operation == ReadComplete {
		return errors.New("transcript read already complete")
	}
	if result.Error != nil && !(reader.operation == ReadAt && errors.Is(result.Error, io.EOF)) {
		reader.err = result.Error
		if reader.operation == ReadAt {
			reader.operation = ReadClose
		} else {
			reader.operation = ReadComplete
		}
		return nil
	}
	switch reader.operation {
	case ReadStat:
		reader.size = result.Size
		reader.operation = ReadWhole
		if result.Size > SkipPrecompactThreshold {
			reader.operation = ReadOpen
		}
	case ReadWhole:
		reader.data = result.Bytes
		reader.finish()
	case ReadOpen:
		reader.offset = 0
		reader.operation = ReadAt
	case ReadAt:
		if int64(len(result.Bytes)) > reader.size-reader.offset {
			return errors.New("physical transcript read exceeded request")
		}
		reader.offset += int64(len(result.Bytes))
		if reader.mode == 0 {
			reader.large.feed(result.Bytes)
		} else {
			reader.meta.feed(result.Bytes)
		}
		if len(result.Bytes) == 0 || reader.offset >= reader.size {
			reader.operation = ReadClose
			if reader.mode == 0 {
				reader.large.finish()
			} else {
				reader.metadata = reader.meta.finish()
			}
		}
	case ReadClose:
		if reader.err != nil {
			reader.operation = ReadComplete
			return nil
		}
		if reader.mode == 0 {
			reader.data = reader.large.result.data
			if reader.large.result.boundary > 0 {
				reader.mode = 1
				reader.size = reader.large.result.boundary
				reader.operation = ReadOpen
				return nil
			}
		}
		reader.finish()
	}
	return nil
}

func (reader *ReadContinuation) finish() {
	if !reader.keep && !reader.large.result.preserved && !reader.disabled && len(reader.data) > SkipPrecompactThreshold {
		reader.data = walkBeforeParse(reader.data)
	}
	reader.operation = ReadComplete
}

// FinishRead uses the existing complete transcript reducer, including partial
// parse, progress bridges, metadata, preserved segments, snips and leaf logic.
// Physical read failures are swallowed at the original Source boundary.
func (loader *Loader) FinishRead(reader *ReadContinuation) (*Transcript, error) {
	if reader == nil || reader.loader != loader || reader.operation != ReadComplete {
		return nil, errors.New("transcript reducer requires its completed read")
	}
	return loader.reduceTranscript(reader.data, reader.metadata, reader.err)
}
