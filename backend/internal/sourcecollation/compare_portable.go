//go:build !darwin && !linux && !windows

package sourcecollation

func New() (*Comparator, error) { return compatibleComparator(), nil }
