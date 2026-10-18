package sessionhistory

// ReadPrimitive describes the original Source API family for the current read
// request. It is not part of ReadRequest, a persisted shape or physical I/O.
type ReadPrimitive uint8

const (
	ReadPrimitiveNone ReadPrimitive = iota
	ReadPrimitiveFSFunction
	ReadPrimitiveFileHandle
	ReadPrimitiveFileHandleClose
	ReadPrimitiveStream
)

// NativePrimitive is sampled beside Request on its existing owner. The first
// large-file pass uses fs/promises.open and FileHandle.read/close. Its second
// metadata pass uses createReadStream and callback-based descriptor I/O instead.
// Looking at a later, already advanced mode would misclassify that same request.
func (reader *ReadContinuation) NativePrimitive() ReadPrimitive {
	if reader == nil || reader.operation == ReadComplete {
		return ReadPrimitiveNone
	}
	switch reader.operation {
	case ReadStat, ReadWhole:
		return ReadPrimitiveFSFunction
	case ReadOpen, ReadAt, ReadClose:
		if reader.mode == 1 {
			return ReadPrimitiveStream
		}
		if reader.operation == ReadOpen {
			return ReadPrimitiveFSFunction
		}
		if reader.operation == ReadClose {
			return ReadPrimitiveFileHandleClose
		}
		return ReadPrimitiveFileHandle
	}
	return ReadPrimitiveNone
}
