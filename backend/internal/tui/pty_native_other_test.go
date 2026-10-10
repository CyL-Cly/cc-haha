//go:build !darwin

package tui

func settleTerminalForObservation(uintptr) (int, error) { return 0, nil }
