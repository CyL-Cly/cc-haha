package clisettings

import (
	"encoding/binary"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"golang.org/x/text/unicode/norm"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// FileSystem injects filesystem capabilities, not parser/merge business.
// WriteFlush must synchronously write and fsync before returning.
type FileSystem interface {
	OpenReadFile(string) (ReadHandle, error)
	ReadFile(string) ([]byte, error)
	Stat(string) (os.FileInfo, error)
	Lstat(string) (os.FileInfo, error)
	EvalSymlinks(string) (string, error)
	Readlink(string) (string, error)
	MkdirAll(string) error
	WriteFlush(string, []byte) error
	Chmod(string, os.FileMode) error
	Rename(string, string) error
	Remove(string) error
}

// ReadHandle borrows one real read-only descriptor for Source's encoding sample.
// ReadAt is a positioned read; it must not use a full-file read as its input.
type ReadHandle interface {
	Fd() uintptr
	ReadAt([]byte, int64) (int, error)
	Close() error
}

type OSFileSystem struct{}

func (OSFileSystem) OpenReadFile(p string) (ReadHandle, error) { return os.Open(p) }
func (OSFileSystem) ReadFile(p string) ([]byte, error)         { return os.ReadFile(p) }
func (OSFileSystem) Stat(p string) (os.FileInfo, error)        { return os.Stat(p) }
func (OSFileSystem) Lstat(p string) (os.FileInfo, error)       { return os.Lstat(p) }
func (OSFileSystem) EvalSymlinks(p string) (string, error)     { return filepath.EvalSymlinks(p) }
func (OSFileSystem) Readlink(p string) (string, error)         { return os.Readlink(p) }
func (OSFileSystem) MkdirAll(p string) error                   { return os.MkdirAll(p, 0777) }
func (OSFileSystem) WriteFlush(p string, data []byte) error {
	file, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
func (OSFileSystem) Chmod(p string, m os.FileMode) error { return os.Chmod(p, m) }
func (OSFileSystem) Rename(a, b string) error            { return os.Rename(a, b) }
func (OSFileSystem) Remove(p string) error               { return os.Remove(p) }
func resolveReadPath(fs FileSystem, path string) (string, error) {
	if strings.HasPrefix(path, "//") || strings.HasPrefix(path, `\\`) {
		return "", &CapabilityError{"UNC settings path"}
	}
	info, err := fs.Lstat(path)
	if err != nil {
		return path, nil
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice) != 0 {
		return "", &CapabilityError{"special settings file"}
	}
	resolved, err := fs.EvalSymlinks(path)
	if err == nil {
		path = norm.NFC.String(resolved)
	}
	return path, nil
}

func readEncodingSample(fs FileSystem, path string) (buffer []byte, bytesRead int, resultErr error) {
	file, err := fs.OpenReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	fd := file.Fd()
	// Source's finally uses if(fd), retaining its distinct fd=0 behavior.
	defer func() {
		if fd != 0 {
			if err := file.Close(); err != nil {
				resultErr = err
			}
		}
	}()
	buffer = make([]byte, 4096)
	bytesRead, resultErr = file.ReadAt(buffer, 0)
	if resultErr == io.EOF {
		resultErr = nil
	}
	return
}

func readContent(fs FileSystem, path string) (string, error) {
	// A fixture can observe the actual helper entrance separately from native
	// OpenReadFile/ReadAt/Close/ReadFile. This never supplies a business result.
	if observer, ok := fs.(interface{ ContentReadStarted(string) }); ok {
		observer.ContentReadStarted(path)
	}
	path, err := resolveReadPath(fs, path)
	if err != nil {
		return "", err
	}
	sample, n, err := readEncodingSample(fs, path)
	if err != nil {
		return "", err
	}
	utf16le := n >= 2 && sample[0] == 255 && sample[1] == 254
	raw, err := fs.ReadFile(path)
	if err != nil {
		return "", err
	}
	var units []uint16
	if utf16le {
		for index := 0; index+1 < len(raw); index += 2 {
			units = append(units, binary.LittleEndian.Uint16(raw[index:index+2]))
		}
	} else {
		units = utf16.Encode([]rune(string(storage.DecodeUTF8(raw))))
	}
	// UTF-16 source preserves lone units inside quoted JSON without prematurely
	// converting numbers or valid/invalid source text into JSON.stringify output.
	content := protocol.JavaScriptJSONSourceUTF16(units)
	return strings.ReplaceAll(content, "\r\n", "\n"), nil
}
func (s *Service) writePhysical(path string, data []byte) error {
	target := path
	if link, err := s.options.FS.Readlink(path); err == nil {
		if filepath.IsAbs(link) {
			target = link
		} else {
			target = filepath.Join(filepath.Dir(path), link)
		}
	}
	temp := fmt.Sprintf("%s.tmp.%d.%.0f", target, s.options.PID, s.options.Now())
	info, err := s.options.FS.Stat(target)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	write := func(path string) error {
		s.emit(Effect{Kind: "writeFlush", Path: path, Bytes: string(data), Options: map[string]any{"encoding": "utf-8", "flush": true}})
		return s.options.FS.WriteFlush(path, data)
	}
	atomicErr := write(temp)
	if atomicErr == nil && exists {
		s.emit(Effect{Kind: "chmod", Path: temp, Mode: uint32(info.Mode().Perm()) | 0100000})
		atomicErr = s.options.FS.Chmod(temp, info.Mode())
	}
	if atomicErr == nil {
		s.emit(Effect{Kind: "rename", Source: temp, Target: target})
		atomicErr = s.options.FS.Rename(temp, target)
	}
	if atomicErr == nil {
		return nil
	}
	s.emit(Effect{Kind: "unlink", Path: temp})
	_ = s.options.FS.Remove(temp)
	return write(target)
}
