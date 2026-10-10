package diagnostics

import (
	"errors"
	"testing"
)

func TestElapsedClockUsesSameProcessNativeOrOriginalErrorBoundary(t *testing.T) {
	native := 0
	p := &Process{options: Options{NowMillis: func() int64 { native++; return 0 }}}
	if got, e := p.ReadNowMillis(); got != 0 || e != nil || native != 1 {
		t.Fatalf("default=%v,%v calls=%d", got, e, native)
	}
	original := errors.New("original process clock error")
	calls := 0
	p.options.ReadNowMillis = func() (float64, error) { calls++; return -500, original }
	if got, e := p.ReadNowMillis(); got != -500 || e != original || calls != 1 || native != 1 {
		t.Fatalf("original=%v,%v calls=%d native=%d", got, e, calls, native)
	}
}
