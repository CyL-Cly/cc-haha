// Package adapterconfig owns the original private, unmasked adapter file read.
// It does not migrate, rewrite, configure, activate, or authenticate adapters.
package adapterconfig

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

type Error struct {
	StatusCode    int
	Code, Message string
}

func (e *Error) Error() string { return e.Message }

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "raw adapter config capability is not migrated: " + e.Boundary
}

type Options struct {
	ConfigDir string
	ReadFile  func(string) ([]byte, error)
	LogError  func(error)
}
type Reader struct{ options Options }

func New(o Options) (*Reader, error) {
	if !filepath.IsAbs(o.ConfigDir) || o.ReadFile == nil || o.LogError == nil {
		return nil, errors.New("raw adapter config requires actual private path/file/error producers")
	}
	return &Reader{o}, nil
}

// ReadJSON preserves the original JSON result's entire shape, including null,
// arrays and primitives, for other adapter consumers. Empty/BOM/corrupt files
// are errors here; the original getter never applies cron-file blank tolerance.
func (r *Reader) ReadJSON(ctx context.Context) (json.RawMessage, error) {
	if e := ctx.Err(); e != nil {
		return nil, context.Cause(ctx)
	}
	data, e := r.options.ReadFile(filepath.Join(r.options.ConfigDir, "adapters.json"))
	if errors.Is(e, fs.ErrNotExist) {
		return []byte(`{}`), nil
	}
	if e == nil {
		data = protocol.DecodeJavaScriptUTF8(data)
		if json.Valid(data) {
			return protocol.StringifyValidatedJavaScriptJSON(data), nil
		}
		message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(string(data))
		if invalid {
			units, _ := protocol.DecodeJavaScriptJSONString(message)
			e = errors.New(string(utf16.Decode(units)))
		} else {
			e = errors.New("Invalid JSON")
		}
	}
	r.options.LogError(e)
	return nil, &Error{500, "INTERNAL_ERROR", "Failed to read adapter config"}
}
func (r *Reader) GetRawConfig(ctx context.Context) (protocol.JSONObject, error) {
	data, e := r.ReadJSON(ctx)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	var object protocol.JSONObject
	if e = object.UnmarshalJSON(data); e != nil {
		return object, &CapabilityError{"non-object adapter config property consumer"}
	}
	return object, nil
}
