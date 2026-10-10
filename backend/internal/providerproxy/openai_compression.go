package providerproxy

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/klauspost/compress/zstd"
)

// OpenAIEncode runs only the immutable byte encoder. A native caller binds
// this CPU operation to its existing demanded I/O completion, never a model.
type OpenAIEncode func([]byte) ([]byte, error)

type OpenAIEncodedBody struct {
	Wire       []byte
	Plain      []byte
	Encoded    bool
	Encoding   string
	PlainBytes int
	WireBytes  int
}

func EncodeOpenAIZstd(plain []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(3)), zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(false))
	if err != nil {
		return nil, err
	}
	defer encoder.Close()
	return encoder.EncodeAll(plain, nil), nil
}

// EncodeOpenAIRequestBody preserves the source's explicit opt-out and local
// encoding fallback. A failed submitted HTTP request is never replayed here.
func EncodeOpenAIRequestBody(ctx context.Context, plain []byte, headers *protocol.JSONObject, optOut string, encoder OpenAIEncode) (OpenAIEncodedBody, error) {
	if err := context.Cause(ctx); err != nil {
		return OpenAIEncodedBody{}, err
	}
	result := OpenAIEncodedBody{Wire: append([]byte(nil), plain...), Plain: append([]byte(nil), plain...), PlainBytes: len(plain), WireBytes: len(plain)}
	if OpenAIRequestCompressionDisabled(optOut) || headers.Get("content-encoding") != nil || encoder == nil {
		return result, nil
	}
	wire, err := encoder(append([]byte(nil), plain...))
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return OpenAIEncodedBody{}, cause
		}
		return result, nil
	}
	if err := context.Cause(ctx); err != nil {
		return OpenAIEncodedBody{}, err
	}
	result.Wire, result.WireBytes, result.Encoded, result.Encoding = wire, len(wire), true, "zstd"
	headers.Set("content-encoding", requestEncode("zstd"))
	headers.Delete("content-length")
	return result, nil
}
