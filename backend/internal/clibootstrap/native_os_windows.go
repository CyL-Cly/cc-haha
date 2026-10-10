//go:build windows

package clibootstrap

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func nativeOSInfo() (systemprompt.OSInfo, error) {
	version := windows.RtlGetVersion()
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return systemprompt.OSInfo{}, err
	}
	defer key.Close()
	name, _, err := key.GetStringValue("ProductName")
	if err != nil {
		return systemprompt.OSInfo{}, err
	}
	return systemprompt.OSInfo{Type: "Windows_NT", Version: name, Release: fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber)}, nil
}
