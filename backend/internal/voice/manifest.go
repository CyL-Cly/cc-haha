package voice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"os"
	"path/filepath"
)

type VerifiedFile struct {
	Name   string  `json:"name"`
	Bytes  float64 `json:"bytes"`
	raw    protocol.JSONObject
	SHA256 string `json:"sha256"`
}

func ReadManifest(path string) []VerifiedFile {
	empty := []VerifiedFile{}
	data, e := os.ReadFile(path)
	if e != nil {
		return empty
	}
	var record struct {
		Version float64           `json:"version"`
		Files   []json.RawMessage `json:"files"`
	}
	if json.Unmarshal(data, &record) != nil || record.Version != 1 || record.Files == nil {
		return empty
	}
	for _, data := range record.Files {
		fields := object(data)
		var bytes float64
		var name, hash string
		if !isManifestString(fields.Get("name")) || !isManifestString(fields.Get("sha256")) || !isManifestNumber(fields.Get("bytes")) || json.Unmarshal(fields.Get("name"), &name) != nil || json.Unmarshal(fields.Get("bytes"), &bytes) != nil || json.Unmarshal(fields.Get("sha256"), &hash) != nil {
			continue
		}
		empty = append(empty, VerifiedFile{Name: name, Bytes: bytes, SHA256: hash, raw: fields.Clone()})
	}
	return empty
}
func RecordVerified(path string, entries []VerifiedFile) error {
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name] = true
	}
	kept := []VerifiedFile{}
	for _, entry := range ReadManifest(path) {
		if !names[entry.Name] {
			kept = append(kept, entry)
		}
	}
	value, e := json.Marshal(struct {
		Version int            `json:"version"`
		Files   []VerifiedFile `json:"files"`
	}{1, append(kept, entries...)})
	var data bytes.Buffer
	if e == nil {
		e = json.Indent(&data, protocol.StringifyValidatedJavaScriptJSON(value), "", "  ")
	}
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, append(data.Bytes(), '\n'), 0666); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func HashFile(ctx context.Context, path string) (string, error) {
	file, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 64*1024)
	for {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		n, e := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func matchesRecord(entry *VerifiedFile, path string, expected *int64) bool {
	if entry == nil || expected != nil && entry.Bytes != float64(*expected) {
		return false
	}
	stat, e := os.Stat(path)
	return e == nil && stat.Mode().IsRegular() && float64(stat.Size()) == entry.Bytes
}
func DescribeFile(ctx context.Context, name, path, digest string) (VerifiedFile, error) {
	stat, e := os.Stat(path)
	if e != nil {
		return VerifiedFile{}, e
	}
	if digest == "" {
		digest, e = HashFile(ctx, path)
	}
	return VerifiedFile{Name: name, Bytes: float64(stat.Size()), SHA256: digest}, e
}
func verifiedMap(layout Layout) map[string]VerifiedFile {
	out := map[string]VerifiedFile{}
	for _, entry := range ReadManifest(layout.Manifest) {
		out[entry.Name] = entry
	}
	return out
}
func IsInstalled(layout Layout, items []Item) bool {
	records := verifiedMap(layout)
	for _, item := range items {
		entry, ok := records[ManifestName(layout, item)]
		if !ok {
			return false
		}
		var expected *int64
		if item.Kind == "file" {
			expected = &item.Asset.Bytes
		}
		if !matchesRecord(&entry, ProofPath(layout, item), expected) {
			return false
		}
	}
	return true
}
func BytesOnDisk(layout Layout, items []Item) int64 {
	records := verifiedMap(layout)
	var total int64
	for _, item := range items {
		entry, ok := records[ManifestName(layout, item)]
		var expected *int64
		if item.Kind == "file" {
			expected = &item.Asset.Bytes
		}
		if ok && matchesRecord(&entry, ProofPath(layout, item), expected) {
			total += item.Asset.Bytes
			continue
		}
		if stat, e := os.Stat(DownloadPath(layout, item) + ".part"); e == nil {
			total += stat.Size()
		}
	}
	return total
}

func (entry VerifiedFile) MarshalJSON() ([]byte, error) {
	if entry.raw.IsObject() {
		return entry.raw.MarshalJSON()
	}
	return protocol.NewJSONObject(field("name", entry.Name), field("bytes", entry.Bytes), field("sha256", entry.SHA256)).MarshalJSON()
}

func isManifestString(data json.RawMessage) bool { return len(data) > 0 && data[0] == '"' }
func isManifestNumber(data json.RawMessage) bool {
	return len(data) > 0 && (data[0] == '-' || data[0] >= '0' && data[0] <= '9')
}

func (entry *VerifiedFile) UnmarshalJSON(data []byte) error {
	var value struct {
		Name   string  `json:"name"`
		Bytes  float64 `json:"bytes"`
		SHA256 string  `json:"sha256"`
	}
	if e := json.Unmarshal(data, &value); e != nil {
		return e
	}
	entry.Name = value.Name
	entry.Bytes = value.Bytes
	entry.SHA256 = value.SHA256
	entry.raw = object(data).Clone()
	return nil
}
