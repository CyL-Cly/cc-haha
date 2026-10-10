package analytics

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
)

// OSFiles retains the original telemetry JSONL layout. No new persisted schema
// marker is introduced; unknown event fields survive retry unchanged.
type OSFiles struct{ Check func(string) error }

func (f OSFiles) check(path string) error {
	if f.Check != nil {
		return f.Check(path)
	}
	return nil
}
func (f OSFiles) ReadFile(path string) ([]byte, error) {
	if e := f.check(path); e != nil {
		return nil, e
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	stat, e := file.Stat()
	if e != nil {
		return nil, e
	}
	const maxBytes = 100 * 1024 * 1024
	if stat.Size() <= maxBytes {
		return io.ReadAll(file)
	}
	raw := make([]byte, maxBytes)
	n, e := file.ReadAt(raw, stat.Size()-maxBytes)
	if e != nil && e != io.EOF {
		return nil, e
	}
	raw = raw[:n]
	if newline := bytes.IndexByte(raw, '\n'); newline >= 0 && newline < len(raw)-1 {
		raw = raw[newline+1:]
	}
	return raw, nil
}
func (f OSFiles) ReadDirNames(path string) ([]string, error) {
	if e := f.check(path); e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(path)
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, e
}
func (f OSFiles) Append(path string, raw []byte) error {
	if e := f.check(path); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	file, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer file.Close()
	_, e = file.Write(raw)
	return e
}
func (f OSFiles) Write(path string, raw []byte) error {
	if e := f.check(path); e != nil {
		return e
	}
	if len(raw) == 0 {
		return f.Remove(path)
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	return os.WriteFile(path, raw, 0600)
}
func (f OSFiles) Remove(path string) error {
	if e := f.check(path); e != nil {
		return e
	}
	return os.Remove(path)
}
