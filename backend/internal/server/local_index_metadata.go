package server

import "github.com/NanmiCoder/cc-haha/backend/internal/localindex"

// NativeIndexSourceMetadata shares the same physical birth/mtime adapter as
// canonical transcript metadata. A missing source remains an actual I/O error.
func NativeIndexSourceMetadata(path string) (localindex.SourceMetadata, error) {
	stat, err := projectionStat(path)
	if err != nil {
		return localindex.SourceMetadata{}, err
	}
	return localindex.SourceMetadata{BirthtimeISO: stat.BirthtimeISO, MtimeISO: stat.MtimeISO}, nil
}
