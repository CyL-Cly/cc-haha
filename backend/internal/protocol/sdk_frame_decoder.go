package protocol

// SDKFrameDecoder is the existing owner demand parser contract. Physical reads
// still return immutable bytes through the single original response lease.
type SDKFrameDecoder interface {
	Next() (SDKSSEEvent, error)
	PollChunk() ([]byte, bool, error)
	AcceptRead([]byte, error)
	PollFrame() (SDKSSEEvent, bool)
	DecodeChunk([]byte)
	FlushLines()
}
