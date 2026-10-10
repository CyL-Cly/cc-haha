//go:build !windows

package sessionstore

import (
	"os"
	"reflect"
)

func fileIdentity(_ *os.File, info os.FileInfo) (string, string, error) {
	value := reflect.Indirect(reflect.ValueOf(info.Sys()))
	dev, err := unsignedField(value, "Dev")
	if err != nil {
		return "", "", err
	}
	ino, err := unsignedField(value, "Ino")
	return dev, ino, err
}
