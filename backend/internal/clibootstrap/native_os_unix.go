//go:build !windows

package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"golang.org/x/sys/unix"
)

func unameText[T ~int8 | ~uint8](value []T) string {
	data := make([]byte, 0, len(value))
	for _, unit := range value {
		if unit == 0 {
			break
		}
		data = append(data, byte(unit))
	}
	return string(data)
}
func nativeOSInfo() (systemprompt.OSInfo, error) {
	var value unix.Utsname
	if err := unix.Uname(&value); err != nil {
		return systemprompt.OSInfo{}, err
	}
	return systemprompt.OSInfo{Type: unameText(value.Sysname[:]), Release: unameText(value.Release[:])}, nil
}
