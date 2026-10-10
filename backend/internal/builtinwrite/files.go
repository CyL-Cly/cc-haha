package builtinwrite

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
)

// OSFiles reuses the existing native flushed writer rather than a second writer.
type OSFiles struct{ clisettings.OSFileSystem }

func (f OSFiles) OpenRead(p string) (ReadHandle, error) { return f.OpenReadFile(p) }
func (f OSFiles) Realpath(p string) (string, error)     { return f.EvalSymlinks(p) }
func (f OSFiles) Mkdir(p string) error {
	e := f.MkdirAll(p)
	if errors.Is(e, fs.ErrExist) {
		return nil
	}
	return e
}
func (OSFiles) Unlink(p string) error {
	if e := syscall.Unlink(p); e != nil {
		return &os.PathError{Op: "unlink", Path: p, Err: e}
	}
	return nil
}

var _ Files = OSFiles{}

type metadata struct {
	content  []uint16
	encoding string
}

func resolveReadPath(f Files, path string) (string, error) {
	info, e := f.Lstat(path)
	if e != nil {
		return path, nil
	}
	if info.Mode()&(fs.ModeNamedPipe|fs.ModeSocket|fs.ModeDevice) != 0 {
		return "", capability("special file metadata read")
	}
	resolved, e := f.Realpath(path)
	if e == nil {
		path = norm.NFC.String(resolved)
	}
	return path, nil
}
func encodingSample(f Files, path string) (buffer []byte, n int, err error) {
	h, e := f.OpenRead(path)
	if e != nil {
		return nil, 0, e
	}
	fd := h.Fd()
	// Source finally closes a truthy descriptor; Go EOF is the raw short-read
	// result of ReadAt, explicitly adapted to Source readSync's successful n.
	defer func() {
		if fd != 0 {
			if e := h.Close(); e != nil {
				err = e
			}
		}
	}()
	buffer = make([]byte, 4096)
	n, err = h.ReadAt(buffer, 0)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return
}
func normalizeCRLF(units []uint16) []uint16 {
	out := make([]uint16, 0, len(units))
	for i, u := range units {
		if u == '\r' && i+1 < len(units) && units[i+1] == '\n' {
			continue
		}
		out = append(out, u)
	}
	return out
}
func readMetadata(f Files, path string) (metadata, error) {
	path, e := resolveReadPath(f, path)
	if e != nil {
		return metadata{}, e
	}
	sample, n, e := encodingSample(f, path)
	if e != nil {
		return metadata{}, e
	}
	enc := "utf8"
	if n >= 2 && sample[0] == 0xff && sample[1] == 0xfe {
		enc = "utf16le"
	}
	data, e := f.ReadFile(path)
	if e != nil {
		return metadata{}, e
	}
	var units []uint16
	if enc == "utf16le" {
		units = make([]uint16, len(data)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(data[i*2:])
		}
	} else {
		if !utf8.Valid(data) {
			return metadata{}, capability("invalid UTF8 native metadata decoder")
		}
		units = utf16.Encode([]rune(string(data)))
	}
	return metadata{normalizeCRLF(units), enc}, nil
}
func contentBytes(content []uint16, enc string) []byte {
	if enc == "utf16le" {
		b := make([]byte, len(content)*2)
		for i, u := range content {
			binary.LittleEndian.PutUint16(b[i*2:], u)
		}
		return b
	}
	return []byte(string(utf16.Decode(content)))
}
func fileMtime(f Files, path string) (float64, error) {
	info, e := f.Stat(path)
	if e != nil {
		return 0, e
	}
	return float64(info.ModTime().UnixMilli()), nil
}
func (s *Service) atomicWrite(path string, data []byte) error {
	f := s.options.Files
	target := path
	if link, e := f.Readlink(path); e == nil {
		if filepath.IsAbs(link) {
			target = link
		} else {
			target = filepath.Join(filepath.Dir(path), link)
		}
	}
	temp := fmt.Sprintf("%s.tmp.%d.%.0f", target, s.options.PID, s.options.NowMillis())
	info, e := f.Stat(target)
	exists := e == nil
	if e != nil && !errors.Is(e, fs.ErrNotExist) {
		return e
	}
	atomicError := f.WriteFlush(temp, data)
	if atomicError == nil && exists {
		atomicError = f.Chmod(temp, info.Mode())
	}
	if atomicError == nil {
		atomicError = f.Rename(temp, target)
	}
	if atomicError == nil {
		return nil
	}
	// Source's cleanup is best effort; fallback's error overrides atomic error.
	_ = f.Unlink(temp)
	return f.WriteFlush(target, data)
}
func contentRaw(units []uint16) []byte { return protocol.QuoteJavaScriptUTF16(units) }
