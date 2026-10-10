//go:build !darwin && !linux && !windows

package mcpapi

func newNameComparator() (*nameComparator, error) { return compatibleNameComparator(), nil }
