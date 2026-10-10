//go:build !darwin

package application

func nativeTUIPresentationTerminalProbe(uintptr) (int, error) { return 0, nil }
