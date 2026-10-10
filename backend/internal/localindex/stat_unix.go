//go:build !windows

package localindex

import (
	"fmt"
	"os"
	"reflect"
)

func statNumber(v reflect.Value, name string) float64 {
	f := v.FieldByName(name)
	if f.CanUint() {
		return float64(f.Uint())
	}
	if f.CanInt() {
		return float64(f.Int())
	}
	return 0
}
func pathLinkCount(_ string, info os.FileInfo) (uint64, error) {
	v := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if v.Kind() != reflect.Struct {
		return 0, fmt.Errorf("native stat lacks link count")
	}
	f := v.FieldByName("Nlink")
	if f.CanUint() {
		return f.Uint(), nil
	}
	if f.CanInt() {
		return uint64(f.Int()), nil
	}
	return 0, fmt.Errorf("native stat lacks link count")
}
func snapshot(info os.FileInfo) (FileSnapshot, error) {
	v := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if v.Kind() != reflect.Struct {
		return FileSnapshot{}, fmt.Errorf("native stat unavailable")
	}
	mtime := float64(info.ModTime().Unix())*1000 + float64(info.ModTime().Nanosecond())/1e6
	var ctime float64
	found := false
	for _, name := range []string{"Ctimespec", "Ctim"} {
		f := v.FieldByName(name)
		if f.Kind() == reflect.Struct {
			ctime = statNumber(f, "Sec")*1000 + statNumber(f, "Nsec")/1e6
			found = true
			break
		}
	}
	if !found {
		return FileSnapshot{}, fmt.Errorf("native stat lacks ctime")
	}
	var id *string
	if ino := statNumber(v, "Ino"); ino != 0 {
		s := fmt.Sprintf("%.0f:%.0f", statNumber(v, "Dev"), ino)
		id = &s
	}
	return FileSnapshot{info.Size(), mtime, ctime, id}, nil
}
func nativeFileSnapshot(_ *os.File, info os.FileInfo) (FileSnapshot, error) { return snapshot(info) }
