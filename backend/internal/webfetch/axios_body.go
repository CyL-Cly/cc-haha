package webfetch

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"github.com/andybalholm/brotli"
	"io"
	"net/http"
	"strings"
)

// ReadAxiosResponseBody follows the installed Node Axios adapter's case-folded
// selector and createUnzip/finishFlush=Z_SYNC_FLUSH behavior. Bun fetch's
// encoding selection is a separate consumer and remains unchanged.
func ReadAxiosResponseBody(response *http.Response) ([]byte, error) {
	wire, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	encoding := strings.ToLower(response.Header.Get("Content-Encoding"))
	var reader io.Reader
	var decoder io.ReadCloser
	switch encoding {
	case "gzip", "x-gzip", "compress", "x-compress":
		if len(wire) >= 2 && wire[0] == 0x1f && wire[1] == 0x8b {
			decoder, err = gzip.NewReader(bytes.NewReader(wire))
		} else {
			decoder, err = zlib.NewReader(bytes.NewReader(wire))
		}
	case "deflate":
		if len(wire) > 0 && wire[0]&15 == 8 {
			decoder, err = zlib.NewReader(bytes.NewReader(wire))
		} else {
			decoder = flate.NewReader(bytes.NewReader(wire))
		}
	case "br":
		reader = brotli.NewReader(bytes.NewReader(wire))
	default:
		return wire, nil
	}
	if err != nil {
		return nil, axiosEntityError(encoding, wire, err)
	}
	if decoder != nil {
		defer decoder.Close()
		reader = decoder
	}
	output, err := io.ReadAll(reader)
	// Axios's Z_SYNC_FLUSH preserves available output when only the end marker
	// or checksum trailer is missing. It still rejects structural codec errors.
	if errors.Is(err, io.ErrUnexpectedEOF) && encoding != "br" {
		err = nil
	}
	if err != nil {
		return nil, axiosEntityError(encoding, wire, err)
	}
	return output, nil
}
func axiosEntityError(encoding string, wire []byte, err error) error {
	message := err.Error()
	switch {
	case encoding == "br":
		message = "Decompression failed"
	case errors.Is(err, gzip.ErrHeader), errors.Is(err, zlib.ErrHeader):
		message = "incorrect header check"
	case errors.Is(err, gzip.ErrChecksum), errors.Is(err, zlib.ErrChecksum):
		message = "incorrect data check"
	case errors.Is(err, zlib.ErrDictionary):
		message = "Missing dictionary"
	default:
		var corrupt flate.CorruptInputError
		if errors.As(err, &corrupt) && encoding == "deflate" && len(wire) > 0 && wire[0]&6 == 6 {
			message = "invalid block type"
		}
	}
	return fmt.Errorf("%s", message)
}
