package clibootstrap

import (
	"math"
	"time"
)

// Source sorts stat.mtime/birthtime Date.getTime(), whose TimeClip truncates
// sub-millisecond filesystem observations, rather than comparing mtimeMs.
func nativeResumeDateMillis(value time.Time) float64 {
	return math.Trunc(float64(value.Unix())*1000 + float64(value.Nanosecond())/1e6)
}
