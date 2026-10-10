package sessioncontroller

import (
	"io"
	"io/fs"
	"os"
	"reflect"
)

// TranscriptFile keeps the descriptor used for identity checking and writing.
// Tests may wrap the operations but do not replace Project business decisions.
type TranscriptFile interface {
	Stat() (fs.FileInfo, error)
	Write([]byte) (int, error)
	Close() error
	Chmod(fs.FileMode) error
}
type TranscriptFiles interface {
	OpenFile(string, int, fs.FileMode) (TranscriptFile, error)
	Stat(string) (fs.FileInfo, error)
	MkdirAll(string, fs.FileMode) error
	ReadFile(string) ([]byte, error)
}
type OSFiles struct{}

func (OSFiles) OpenFile(path string, flag int, mode fs.FileMode) (TranscriptFile, error) {
	return os.OpenFile(path, flag|noFollowFlag(), mode)
}
func (OSFiles) Stat(path string) (fs.FileInfo, error)        { return os.Stat(path) }
func (OSFiles) MkdirAll(path string, mode fs.FileMode) error { return os.MkdirAll(path, mode) }
func (OSFiles) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }

type identity struct{ device, inode float64 }

func fileIdentity(info fs.FileInfo) (identity, error) {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.IsValid() && value.Kind() == reflect.Struct {
		number := func(name string) (float64, bool) {
			field := value.FieldByName(name)
			if !field.IsValid() {
				return 0, false
			}
			switch field.Kind() {
			case reflect.Uint, reflect.Uint64, reflect.Uint32:
				return float64(field.Uint()), true
			case reflect.Int, reflect.Int64, reflect.Int32:
				return float64(field.Int()), true
			}
			return 0, false
		}
		device, a := number("Dev")
		inode, b := number("Ino")
		if a && b {
			return identity{device, inode}, nil
		}
	}
	// Windows' Source descriptor identity is not yet exposed by Go FileInfo.Sys.
	// Do not replace it with path/mtime equality and risk writing a replaced file.
	return identity{}, capability("native descriptor dev/ino on this host")
}
func writeAll(file TranscriptFile, data []byte) error {
	for len(data) > 0 {
		n, err := file.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
