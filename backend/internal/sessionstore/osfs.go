package sessionstore

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
)

type OSFileSystem struct{}
type osFile struct{ *os.File }

func (OSFileSystem) Open(path string) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &osFile{file}, nil
}
func (file *osFile) Stat() (Snapshot, error) {
	info, err := file.File.Stat()
	if err != nil {
		return Snapshot{}, err
	}
	dev, ino, err := fileIdentity(file.File, info)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{dev, ino, strconv.FormatInt(info.ModTime().UnixNano(), 10), info.Size()}, nil
}
func unsignedField(value reflect.Value, name string) (string, error) {
	if value.Kind() != reflect.Struct {
		return "", fmt.Errorf("native stat lacks %s", name)
	}
	field := value.FieldByName(name)
	if field.CanUint() {
		return strconv.FormatUint(field.Uint(), 10), nil
	}
	if field.CanInt() {
		return strconv.FormatInt(field.Int(), 10), nil
	}
	return "", fmt.Errorf("native stat lacks %s", name)
}
