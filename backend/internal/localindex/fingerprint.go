package localindex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strings"
)

const fingerprintWindow = 64 * 1024
const fingerprintPrefix = "cc-haha-source-fingerprint:v2:"

var errSourceChanged = errors.New("Source changed during fingerprint")
var errSourceMissingBeforeFingerprint = errors.New("")

type FileSnapshot struct {
	Size             int64
	MtimeMs, CtimeMs float64
	FileIdentity     *string
}
type Fingerprint struct {
	Size               int64   `json:"size"`
	MtimeMs            float64 `json:"mtimeMs"`
	CtimeMs            float64 `json:"ctimeMs"`
	FileIdentity       *string `json:"fileIdentity"`
	FirstWindowHash    string  `json:"firstWindowHash"`
	LastWindowHash     string  `json:"lastWindowHash"`
	BoundaryWindowHash string  `json:"boundaryWindowHash"`
	IndexedBytes       int64   `json:"indexedBytes"`
	ParserVersion      int64   `json:"parserVersion"`
}
type SourceChange struct {
	Kind     string `json:"kind"`
	Reason   string `json:"reason,omitempty"`
	ReadFrom int64  `json:"readFrom,omitempty"`
}

func (c SourceChange) MarshalJSON() ([]byte, error) {
	m := map[string]any{"kind": c.Kind}
	if c.Reason != "" {
		m["reason"] = c.Reason
	}
	if c.Kind == "append" {
		m["readFrom"] = c.ReadFrom
	}
	return json.Marshal(m)
}

func sameIdentity(a, b *string) bool { return a == nil || b == nil || *a == *b }
func sameSnapshot(a, b FileSnapshot) bool {
	return a.Size == b.Size && a.MtimeMs == b.MtimeMs && a.CtimeMs == b.CtimeMs && sameIdentity(a.FileIdentity, b.FileIdentity)
}
func fileSnapshot(f *os.File) (FileSnapshot, error) {
	s, e := f.Stat()
	if e != nil {
		return FileSnapshot{}, e
	}
	return nativeFileSnapshot(f, s)
}
func FingerprintMatchesInfo(info os.FileInfo, f Fingerprint) bool {
	stat, err := snapshot(info)
	return err == nil && sameSnapshot(stat, FileSnapshot{f.Size, f.MtimeMs, f.CtimeMs, f.FileIdentity})
}
func FingerprintMatchesFile(file *os.File, f Fingerprint) bool {
	stat, err := fileSnapshot(file)
	return err == nil && sameSnapshot(stat, FileSnapshot{f.Size, f.MtimeMs, f.CtimeMs, f.FileIdentity})
}
func FingerprintMatchesPath(path string, f Fingerprint) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	return FingerprintMatchesFile(file, f)
}
func stableHandle(path string, f *os.File, before FileSnapshot) error {
	after, err := fileSnapshot(f)
	if err != nil {
		return err
	}
	namedFile, err := os.Open(path)
	if os.IsNotExist(err) {
		return errSourceChanged
	}
	if err != nil {
		return err
	}
	defer namedFile.Close()
	named, err := fileSnapshot(namedFile)
	if err != nil {
		return err
	}
	if !sameSnapshot(before, after) || !sameSnapshot(after, named) {
		return errSourceChanged
	}
	return nil
}
func hashWindow(ctx context.Context, f *os.File, end int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	length := min(int64(fingerprintWindow), end)
	data := make([]byte, length)
	read, err := f.ReadAt(data, end-length)
	if int64(read) != length {
		return "", errSourceChanged
	}
	if err != nil && err != io.EOF {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
func CaptureFingerprint(ctx context.Context, path string, indexedBytes, parserVersion int64) (*Fingerprint, error) {
	if indexedBytes < 0 || indexedBytes > 9007199254740991 {
		return nil, errors.New("indexedBytes must be a non-negative safe integer")
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errSourceMissingBeforeFingerprint
		}
		return nil, err
	}
	defer file.Close()
	before, err := fileSnapshot(file)
	if err != nil {
		return nil, err
	}
	if indexedBytes > before.Size {
		return nil, errors.New("indexedBytes exceeds source size")
	}
	first, err := hashWindow(ctx, file, min(int64(fingerprintWindow), before.Size))
	if err != nil {
		return nil, err
	}
	last, err := hashWindow(ctx, file, before.Size)
	if err != nil {
		return nil, err
	}
	boundary, err := hashWindow(ctx, file, indexedBytes)
	if err != nil {
		return nil, err
	}
	if err = stableHandle(path, file, before); err != nil {
		return nil, err
	}
	return &Fingerprint{before.Size, before.MtimeMs, before.CtimeMs, before.FileIdentity, first, last, boundary, indexedBytes, parserVersion}, nil
}
func SerializeFingerprint(f Fingerprint) string {
	return fingerprintPrefix + base64.RawURLEncoding.EncodeToString(raw([]any{f.Size, f.MtimeMs, f.CtimeMs, f.FileIdentity, f.FirstWindowHash, f.LastWindowHash, f.BoundaryWindowHash, f.IndexedBytes, f.ParserVersion}))
}
func DeserializeFingerprint(s string) *Fingerprint {
	if !strings.HasPrefix(s, fingerprintPrefix) {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimPrefix(s, fingerprintPrefix), "="))
	if err != nil {
		return nil
	}
	var fields []json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 9 {
		return nil
	}
	var f Fingerprint
	for _, n := range []int{0, 1, 2, 7, 8} {
		if len(fields[n]) == 0 || fields[n][0] == '"' || string(fields[n]) == "null" || string(fields[n]) == "true" || string(fields[n]) == "false" {
			return nil
		}
	}
	var size, indexed, parser float64
	for i, d := range map[int]any{0: &size, 1: &f.MtimeMs, 2: &f.CtimeMs, 3: &f.FileIdentity, 4: &f.FirstWindowHash, 5: &f.LastWindowHash, 6: &f.BoundaryWindowHash, 7: &indexed, 8: &parser} {
		if json.Unmarshal(fields[i], d) != nil {
			return nil
		}
	}
	for _, n := range []float64{size, indexed, parser} {
		if n < 0 || n > 9007199254740991 || math.Trunc(n) != n || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
	}
	f.Size, f.IndexedBytes, f.ParserVersion = int64(size), int64(indexed), int64(parser)
	if f.Size < 0 || f.Size > 9007199254740991 || f.IndexedBytes < 0 || f.IndexedBytes > f.Size || f.ParserVersion < 0 || f.ParserVersion > 9007199254740991 || math.IsNaN(f.MtimeMs) || math.IsInf(f.MtimeMs, 0) || math.IsNaN(f.CtimeMs) || math.IsInf(f.CtimeMs, 0) {
		return nil
	}
	for _, s := range []string{f.FirstWindowHash, f.LastWindowHash, f.BoundaryWindowHash} {
		if len(s) != 64 {
			return nil
		}
		for _, c := range s {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return nil
			}
		}
	}
	return &f
}
func DetectSourceChange(ctx context.Context, path string, previous Fingerprint, parserVersion int64) SourceChange {
	if parserVersion != previous.ParserVersion {
		return SourceChange{Kind: "rebuild", Reason: "parser-version"}
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return SourceChange{Kind: "deleted"}
	}
	if err != nil {
		return SourceChange{Kind: "retry", Reason: "transient-io"}
	}
	defer file.Close()
	current, err := fileSnapshot(file)
	if err != nil {
		return SourceChange{Kind: "retry", Reason: "transient-io"}
	}
	if !sameIdentity(previous.FileIdentity, current.FileIdentity) {
		return SourceChange{Kind: "rebuild", Reason: "replace"}
	}
	if current.Size < previous.Size {
		return SourceChange{Kind: "rebuild", Reason: "truncate"}
	}
	rewritten := false
	for _, window := range []struct {
		end  int64
		hash string
	}{{min(int64(fingerprintWindow), previous.Size), previous.FirstWindowHash}, {previous.Size, previous.LastWindowHash}, {previous.IndexedBytes, previous.BoundaryWindowHash}} {
		h, e := hashWindow(ctx, file, window.end)
		if e != nil {
			return retryChange(e)
		}
		rewritten = rewritten || h != window.hash
	}
	if err = stableHandle(path, file, current); err != nil {
		return retryChange(err)
	}
	if rewritten {
		return SourceChange{Kind: "rebuild", Reason: "rewrite"}
	}
	if current.Size == previous.Size {
		if current.MtimeMs != previous.MtimeMs || current.CtimeMs != previous.CtimeMs {
			return SourceChange{Kind: "rebuild", Reason: "rewrite"}
		}
		return SourceChange{Kind: "unchanged"}
	}
	return SourceChange{Kind: "append", ReadFrom: previous.IndexedBytes}
}
func retryChange(err error) SourceChange {
	reason := "transient-io"
	if errors.Is(err, errSourceChanged) || os.IsNotExist(err) {
		reason = "changed-during-read"
	}
	return SourceChange{Kind: "retry", Reason: reason}
}
func VerifyFingerprint(ctx context.Context, path string, expected Fingerprint) SourceChange {
	actual, err := CaptureFingerprint(ctx, path, expected.IndexedBytes, expected.ParserVersion)
	if err != nil {
		if errors.Is(err, errSourceMissingBeforeFingerprint) {
			return SourceChange{Kind: "retry", Reason: "changed-during-read"}
		}
		return retryChange(err)
	}
	if sameSnapshot(FileSnapshot{actual.Size, actual.MtimeMs, actual.CtimeMs, actual.FileIdentity}, FileSnapshot{expected.Size, expected.MtimeMs, expected.CtimeMs, expected.FileIdentity}) && actual.FirstWindowHash == expected.FirstWindowHash && actual.LastWindowHash == expected.LastWindowHash && actual.BoundaryWindowHash == expected.BoundaryWindowHash {
		return SourceChange{Kind: "unchanged"}
	}
	return SourceChange{Kind: "retry", Reason: "changed-during-read"}
}
