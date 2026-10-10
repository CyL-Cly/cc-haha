package searchapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func strconvI(n int64) string { return strconv.FormatInt(n, 10) }
func sameOptionalString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func matchFitsFingerprint(m localindex.ContentMatch, f localindex.Fingerprint) bool {
	return m.ByteStart >= 0 && m.ByteStart <= 9007199254740991 && m.ByteLength > 0 && m.ByteLength <= localindex.ContentMaxLineBytes && m.ByteStart+m.ByteLength <= f.IndexedBytes && m.SourceSizeBytes == f.Size && m.SourceMtimeMs == f.MtimeMs && sameOptionalString(m.SourceFileIdentity, f.FileIdentity) && m.SourceIndexedBytes == f.IndexedBytes && m.SourceParserVersion == f.ParserVersion && m.LineNumber > 0 && m.LineNumber <= 9007199254740991 && m.SegmentIndex >= 0 && m.SegmentIndex <= 9007199254740991
}
func readCanonicalEntry(ctx context.Context, path string, start, length int64, fingerprint localindex.Fingerprint) (protocol.JSONObject, error) {
	entry := protocol.NewJSONObject()
	if ctx.Err() != nil {
		return entry, ctx.Err()
	}
	file, e := os.Open(path)
	if e != nil {
		return entry, e
	}
	defer file.Close()
	changed := errors.New("Indexed canonical entry changed")
	if !localindex.FingerprintMatchesFile(file, fingerprint) {
		return entry, changed
	}
	data := make([]byte, length)
	read, e := file.ReadAt(data, start)
	if e != nil || int64(read) != length {
		return entry, changed
	}
	if ctx.Err() != nil {
		return entry, ctx.Err()
	}
	if !localindex.FingerprintMatchesFile(file, fingerprint) || !localindex.FingerprintMatchesPath(path, fingerprint) || len(data) == 0 || data[len(data)-1] != '\n' {
		return entry, changed
	}
	end := len(data) - 1
	if end > 0 && data[end-1] == '\r' {
		end--
	}
	if e = json.Unmarshal(data[:end], &entry); e != nil {
		return entry, e
	}
	return entry, nil
}
