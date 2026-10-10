// Package tracecapture owns the native capture and bounded projection of the
// original canonical trace JSONL. Provider-specific transports only deliver
// actual fetch observations and a separately owned response clone.
package tracecapture

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const (
	PreviewChars         = 240000
	StreamCaptureBytes   = 1024 * 1024
	ListPreviewChars     = 2048
	RecordBytesLimit     = 2 * 1024 * 1024
	WindowRecordLimit    = 10000
	WindowBytesLimit     = 64 * 1024 * 1024
	OverviewLimit        = 100
	LegacyFullBytesLimit = 8 * 1024 * 1024
	CaptureNodeLimit     = 4096
	CaptureDepthLimit    = 32
	CaptureCharLimit     = 1024 * 1024
	ParserVersion        = 2
)

type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

type Settings struct {
	Enabled    bool   `json:"enabled"`
	StorageDir string `json:"storageDir"`
}
type ProviderInfo struct {
	ID     *string `json:"id"`
	Name   string  `json:"name"`
	Format string  `json:"format"`
}
type Binding struct {
	SessionID   string
	QuerySource string
	Provider    *ProviderInfo
	Source      string
}
type BodySnapshot struct {
	ContentType    string          `json:"contentType"`
	Bytes          int64           `json:"bytes"`
	SHA256         string          `json:"sha256"`
	Preview        json.RawMessage `json:"preview"`
	Truncated      bool            `json:"truncated"`
	CaptureOmitted string          `json:"captureOmitted,omitempty"`
}

// FetchInput is delivered once per physical fetch, after final request headers.
// PlainBody retains the uncompressed, original serialized request body.
type FetchInput struct {
	Context          context.Context
	Method, URL      string
	Headers          http.Header
	PlainBody        []byte
	EncodingMetadata protocol.JSONObject
}

// FetchCapture accepts ownership of the separately forked response clone.
// Implementations never read, replace or close the SDK's own body branch.
// HeadersResponse and FetchFailure are mutually exclusive fetch outcomes;
// capture body completion/abort follows asynchronously after HeadersResponse.
type FetchCapture interface {
	HeadersResponse(status int, headers http.Header, independentClone io.ReadCloser)
	FetchFailure(failure error)
}
type BeginFetch func(FetchInput) FetchCapture
