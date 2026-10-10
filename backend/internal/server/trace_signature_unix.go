//go:build !windows

package server

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
)

func traceCTime(path string) (string, error) {
	stat, e := os.Stat(path)
	if e != nil {
		return "", e
	}
	v := reflect.Indirect(reflect.ValueOf(stat.Sys()))
	for _, name := range []string{"Ctimespec", "Ctim"} {
		f := v.FieldByName(name)
		if f.Kind() == reflect.Struct {
			sec, nsec := f.FieldByName("Sec"), f.FieldByName("Nsec")
			if sec.CanInt() && nsec.CanInt() {
				return strconv.FormatInt(sec.Int()*1e9+nsec.Int(), 10), nil
			}
		}
	}
	return "", fmt.Errorf("Native trace signature requires ctime")
}
